# Interlocked SDK
Publish and create timetables for the Interlocked Registry, with the option to visualise layouts to assist with this process.

## Install

Download a prebuilt binary for your platform from the
[latest release](https://github.com/std-semaphore/interlocked-sdk/releases/latest):

```bash
# Linux/macOS example (amd64)
curl -LO https://github.com/std-semaphore/interlocked-sdk/releases/latest/download/intsdk_<version>_<os>_<arch>.tar.gz
tar -xzf intsdk_<version>_<os>_<arch>.tar.gz
sudo mv intsdk /usr/local/bin/
```

On Windows, download the `.zip` for `windows_amd64`, extract it, and put
`intsdk.exe` somewhere on your `PATH`.

Check the exact filename on the
[releases page](https://github.com/std-semaphore/interlocked-sdk/releases) —
it includes the version, OS, and architecture, e.g.
`intsdk_1.2.0_linux_amd64.tar.gz`.

### From source

```bash
git clone https://github.com/std-semaphore/interlocked-sdk.git
cd interlocked-sdk
go build -o intsdk .
```

Building the `map` command requires Fyne's system dependencies. On Ubuntu:

```bash
sudo apt-get install gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev
```

See the [Fyne docs](https://docs.fyne.io/started/) for other platforms.

## Usage

```bash
intsdk register <id>            # create an author account
intsdk publish [path]           # build and upload a timetable
intsdk map [path]               # open the track visualiser (defaults to data/kestby.toml)
intsdk --help                   # full command list
```

## Development

```bash
go mod tidy
go vet ./...
go test ./...
```

### Releasing

Releases are built and published automatically by
[goreleaser](https://goreleaser.com) when a `v*` tag is pushed:

```bash
git tag v1.2.0
git push origin v1.2.0
```

This builds native binaries for Linux, macOS, and Windows (amd64/arm64
where applicable) and publishes them to GitHub Releases.

To test a build locally without tagging:

```bash
goreleaser build --single-target --snapshot --clean
```