rssnip
=======

[![Test Status](https://github.com/Songmu/rssnip/actions/workflows/test.yaml/badge.svg?branch=main)][actions]
[![Coverage Status](https://codecov.io/gh/Songmu/rssnip/branch/main/graph/badge.svg)][codecov]
[![MIT License](https://img.shields.io/github/license/Songmu/rssnip)][license]
[![PkgGoDev](https://pkg.go.dev/badge/github.com/Songmu/rssnip)][PkgGoDev]

[actions]: https://github.com/Songmu/rssnip/actions?workflow=test
[codecov]: https://codecov.io/gh/Songmu/rssnip
[license]: https://github.com/Songmu/rssnip/blob/main/LICENSE
[PkgGoDev]: https://pkg.go.dev/github.com/Songmu/rssnip

rssnip short description

## Synopsis

```go
// simple usage here
```

## Description

## Installation

```console
# Install the latest version. (Install it into ./bin/ by default).
% curl -sfL https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s

# Specify installation directory ($(go env GOPATH)/bin/) and version.
% curl -sfL https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s -- -b $(go env GOPATH)/bin [vX.Y.Z]

# In alpine linux (as it does not come with curl by default)
% wget -O - -q https://raw.githubusercontent.com/Songmu/rssnip/main/install.sh | sh -s [vX.Y.Z]

# go install
% go install github.com/Songmu/rssnip/cmd/rssnip@latest
```

## Author

[Songmu](https://github.com/Songmu)
