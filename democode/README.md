# Archived learning examples

These versioned examples document earlier implementation steps. They are a
separate Go module so `go build ./...` and `go test ./...` at the repository root
cover the current service without compiling historical snapshots.

Some snapshots intentionally use local configuration packages that are ignored
by Git because they once contained API credentials. They are retained for
reference and are not part of the deployable service.
