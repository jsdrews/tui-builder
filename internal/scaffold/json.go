package scaffold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that remembers its key order, so columns come
// out in the order the command prints fields.
type object struct {
	keys []string
	vals map[string]any
}

// maxNDJSONRows is how many lines of NDJSON are sampled as rows.
const maxNDJSONRows = 50

// decodeSample decodes a command's output: one JSON document, or
// several concatenated (NDJSON). Values are *object, []any, string,
// json.Number, bool or nil. Cut-off output keeps every value that
// decoded before the cut.
func decodeSample(out Output) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(out.Stdout))
	dec.UseNumber()
	var values []any
	for len(values) < maxNDJSONRows {
		v, err := decodeValue(dec)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if out.Truncated && len(values) > 0 {
				break
			}
			if out.Truncated {
				return nil, fmt.Errorf("the output ran past %d MB and the part before the cut isn't complete JSON", len(out.Stdout)>>20)
			}
			return nil, notJSON(out.Stdout, len(values) > 0)
		}
		values = append(values, v)
	}
	return values, nil
}

func notJSON(raw []byte, afterValues bool) error {
	where := "the output isn't JSON"
	if afterValues {
		where = "the output has a line that isn't JSON"
	}
	preview := strings.TrimSpace(string(raw))
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	return fmt.Errorf("%s: %q\nAsk the command for JSON (often -o json or --format json); for plain text, use an exec source with format: text", where, preview)
}

// decodeValue reads one value. io.EOF means there was none left; a cut
// inside a value is io.ErrUnexpectedEOF.
func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch d {
	case '{':
		o := &object{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, unexpected(err)
			}
			k, _ := kt.(string)
			v, err := decodeValue(dec)
			if err != nil {
				return nil, unexpected(err)
			}
			if _, dup := o.vals[k]; !dup {
				o.keys = append(o.keys, k)
			}
			o.vals[k] = v
		}
		_, err := dec.Token()
		return o, unexpected(err)
	case '[':
		a := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, unexpected(err)
			}
			a = append(a, v)
		}
		_, err := dec.Token()
		return a, unexpected(err)
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
