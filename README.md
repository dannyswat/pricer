# pricer

A Go library, command-line tool and web interface for theoretical prices of options and structured notes. There are no third-party dependencies.

## Requirements

- Go 1.26.2 or newer (see [go.mod](go.mod))

## Layout

| Path | Purpose |
| --- | --- |
| `bond/` | Risk-free Treasury bond pricing |
| `option/` | Vanilla, digital, barrier and worst-of options |
| `eln/` | Equity-linked notes (vanilla and worst-of), priced as issuer |
| `discount/`, `daycount/` | Discount curves and year-fraction conventions |
| `pricing/` | Shared price result types |
| `cmd/pricer/` | Command-line tool |
| `cmd/web/` | Web interface and JSON API |

## Build

```sh
go build ./...                       # compile everything
go build -o bin/pricer ./cmd/pricer  # CLI binary
go build -o bin/web ./cmd/web        # web server binary
```

## Run

### Command line

```sh
go run ./cmd/pricer help
go run ./cmd/pricer put -strike 95 -vol 0.25
go run ./cmd/pricer down-in-put -strike 100 -barrier 90 -spot 100
go run ./cmd/pricer worst-of-down-in-put -barrier 0.7 -paths 50000
```

Commands: `treasury`, `put`, `call`, `down-in-put`, `digital-call`, `digital-put`, `worst-of-put`, `worst-of-down-in-put`, `eln`, `worst-of-eln`. Run `go run ./cmd/pricer <command> -h` for the flags of each.

### Web interface

```sh
go run ./cmd/web                    # http://127.0.0.1:8080
go run ./cmd/web -addr :9000        # custom address
```

Open the address in a browser, pick a product, and edit the spot, strike, barrier or any other input. Results and the price chart update as you type.

The page in `cmd/web/static/` is embedded into the binary at build time. Restart the server after editing it.

#### API

| Endpoint | Description |
| --- | --- |
| `GET /api/products` | Product catalog: input fields and defaults |
| `POST /api/price` | Price a product |
| `POST /api/curve` | Price against spot, for the chart |

The body is JSON with a `product` id from the catalog. Any other field you omit takes the product's default. Rates, volatilities and dividends are decimals (`0.2` = 20%). For worst-of products, `strike` and `barrier` are fractions of each underlier's initial level.

```sh
curl -s -XPOST localhost:8080/api/price \
  -d '{"product":"down-in-put","spot":100,"strike":105,"barrier":85}'
```

Worst-of products also accept `underliers` (`name`, `spot`, `initial`, `vol`, `dividend`), `correlation`, `paths` (at most 1,000,000) and `seed`.

## Test

```sh
go test ./...              # all packages
go test ./cmd/web          # web API only
go test ./option -run DownIn -v
go vet ./...
gofmt -l .                 # lists unformatted files; should print nothing
```

The Monte Carlo tests use fixed seeds, so results are reproducible.

## Adding a product to the web interface

Add an entry in `products()` in [cmd/web/products.go](cmd/web/products.go) with its input fields, defaults and a price function. The first entry of `Headline` in the response is the value plotted on the chart. The UI is built from the catalog, so no front-end change is needed.
