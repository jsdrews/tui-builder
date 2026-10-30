# Public specs

Real-world OpenAPI documents gen's tests run against (`public_test.go`).
They're checked by assertions, not golden files: the output is too big
to review.

- `petstore.yaml`: the Swagger Petstore, OpenAPI 3.0, from
  [swagger-api/swagger-petstore](https://github.com/swagger-api/swagger-petstore)
  at `5cb909fc7780bc708151bce0014939acba8b212a`, Apache License 2.0.
  Vendored: it's small.
- `.cache/github.yaml` and `.cache/stripe.yaml`: GitHub's REST API
  ([github/rest-api-description](https://github.com/github/rest-api-description),
  MIT) and Stripe's ([stripe/openapi](https://github.com/stripe/openapi),
  MIT), both pinned to a commit. They're 10 MB and 6 MB, so they aren't
  committed: `go generate ./internal/gen` fetches them (`fetch.sh`), and
  the tests skip either when it isn't there.
