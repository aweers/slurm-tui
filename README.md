# SLURM tui

A simple tui for running slurm jobs and their log files.

## Build

- Local binary: `make build`
- Linux amd64: `make linux-amd64`
- Linux arm64: `make linux-arm64`
- Generic release build: `make release GOOS=linux GOARCH=amd64`

Build outputs are written to `dist/` for release targets.

## GitHub Releases

The workflow at `.github/workflows/release.yml` builds Linux binaries for
`amd64` and `arm64` on tag pushes like `v0.1.0` and uploads them as release
assets.
