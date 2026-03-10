# Contributing

Contributions are welcome. Here's what you need to know.

## Development

```bash
make          # build
make test     # run tests
```

Run a single test:

```bash
go test -run TestName ./...
```

Always run tests with the race detector before submitting:

```bash
go test -race ./...
```

## Pull Requests

- Open a PR against `master`.
- All PRs are squash-merged — the PR title becomes the commit message.
- CI must pass (tests on Linux + macOS).

## License

By contributing, you agree that your contributions will be licensed under the
Apache License 2.0.
