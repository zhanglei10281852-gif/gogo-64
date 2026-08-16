# HumpYard release report

Independent original project. No affiliation with, endorsement by, sponsorship
by, or association with any other product, company, or organization.

- Module: `HumpYard`
- Go directive: `go 1.22.5`
- Third-party dependencies: none (standard library only)
- Toolchain policy: `GOTOOLCHAIN=local`, `GOPROXY=off`

## Effective production LOC

Effective LOC counts lines that are neither blank nor comment-only, excluding
`*_test.go` files. No generated code is present.

| File                            | Effective LOC |
| ------------------------------- | ------------- |
| cmd/humpyard/main.go            | 8             |
| internal/blocking/blocking.go   | 335           |
| internal/cli/cli.go             | 469           |
| internal/config/config.go       | 253           |
| internal/config/load.go         | 46            |
| internal/config/validate.go     | 466           |
| internal/depart/depart.go       | 403           |
| internal/hazmat/hazmat.go       | 256           |
| internal/hump/hump.go           | 529           |
| internal/ingest/ingest.go       | 269           |
| internal/jsonx/strict.go        | 135           |
| internal/model/car.go           | 188           |
| internal/model/order.go         | 156           |
| internal/model/resource.go      | 149           |
| internal/model/track.go         | 164           |
| internal/occupancy/occupancy.go | 307           |
| internal/pipeline/pipeline.go   | 249           |
| internal/rehandle/rehandle.go   | 240           |
| internal/report/report.go       | 465           |
| internal/shift/shift.go         | 383           |
| internal/store/audit.go         | 217           |
| internal/store/store.go         | 237           |
| **Total (22 files)**            | **5924**      |

Requirement was at least 2600 effective production LOC.

## Packages

| Package   | Import path                 | Role                                                |
| --------- | --------------------------- | --------------------------------------------------- |
| main      | HumpYard/cmd/humpyard       | CLI entry point                                     |
| jsonx     | HumpYard/internal/jsonx     | Strict JSON/JSONL decoding, canonical encoding      |
| model     | HumpYard/internal/model     | Cars, tracks, power, crews, shifts, yard orders     |
| config    | HumpYard/internal/config    | Configuration document, loader, semantic validation |
| ingest    | HumpYard/internal/ingest    | Yard order loading, statistics, cross-checks        |
| blocking  | HumpYard/internal/blocking  | Destination to block, block to track assignment     |
| hump      | HumpYard/internal/hump      | Crest sequencing, cuts, flat moves                  |
| hazmat    | HumpYard/internal/hazmat    | Hazardous material placement validation             |
| occupancy | HumpYard/internal/occupancy | Bowl occupancy simulation                           |
| depart    | HumpYard/internal/depart    | Outbound train building and manifests               |
| rehandle  | HumpYard/internal/rehandle  | Rework detection, rehandle percentage               |
| shift     | HumpYard/internal/shift     | Task derivation, crew assignment                    |
| store     | HumpYard/internal/store     | Ledger, snapshot, metadata, hash-chained audit log  |
| pipeline  | HumpYard/internal/pipeline  | Stage orchestration and persistence                 |
| report    | HumpYard/internal/report    | Deterministic text and JSON rendering               |
| cli       | HumpYard/internal/cli       | Subcommand dispatch, flags, exit codes              |

16 packages total: 1 command plus 15 internal packages.

## Test files

| File                                 | Focus                                                                |
| ------------------------------------ | -------------------------------------------------------------------- |
| internal/jsonx/strict_test.go        | Unknown fields, trailing values, JSONL splitting, encoding stability |
| internal/model/model_test.go         | Car and train validation, restrictions, drawbar symmetry, ordering   |
| internal/config/config_test.go       | Example load, normalization, semantic validation failures            |
| internal/ingest/ingest_test.go       | Both order formats, record tags, cross-checks                        |
| internal/blocking/blocking_test.go   | Capacity, weight, restrictions, placard ceiling, determinism         |
| internal/hump/hump_test.go           | Flat-switch reasons, cut limits, drawbar pairs, riders, determinism  |
| internal/hazmat/hazmat_test.go       | Buffer spacing, caboose and crew adjacency, placard ceiling          |
| internal/occupancy/occupancy_test.go | Placement order, overflow, refusal, determinism                      |
| internal/depart/depart_test.go       | Block pull order, ceilings, power shortfall, manifests               |
| internal/rehandle/rehandle_test.go   | Misroutes, second passes, category tallies, determinism              |
| internal/shift/shift_test.go         | Task derivation, qualifications, duty limits, load balancing         |
| internal/store/store_test.go         | Ledger sequencing, atomic writes, chain tamper detection             |
| internal/pipeline/pipeline_test.go   | Full chain, snapshot round trip, ledger coverage, determinism        |
| internal/report/report_test.go       | Section rendering, no wall-clock output, formatting helpers          |
| internal/cli/cli_test.go             | Every subcommand, exit codes, JSON output, on-disk reproducibility   |

## Validation results

All commands were run with `GOTOOLCHAIN=local` and `GOPROXY=off`, and with
`GOCACHE` and `GOTMPDIR` redirected under `.cache/`.

| Command                  | Result                           |
| ------------------------ | -------------------------------- |
| `gofmt -l .`             | clean (no output)                |
| `go build ./...`         | pass                             |
| `go vet ./...`           | pass                             |
| `go test ./... -count=1` | pass, 15 packages ok, 0 failures |

Test summary from the final run:

```
?       HumpYard/cmd/humpyard   [no test files]
ok      HumpYard/internal/blocking
ok      HumpYard/internal/cli
ok      HumpYard/internal/config
ok      HumpYard/internal/depart
ok      HumpYard/internal/hazmat
ok      HumpYard/internal/hump
ok      HumpYard/internal/ingest
ok      HumpYard/internal/jsonx
ok      HumpYard/internal/model
ok      HumpYard/internal/occupancy
ok      HumpYard/internal/pipeline
ok      HumpYard/internal/rehandle
ok      HumpYard/internal/report
ok      HumpYard/internal/shift
ok      HumpYard/internal/store
```

## Offline CLI smoke workflow

Run against the bundled example data with no network access:

| Step | Command                                                             | Exit |
| ---- | ------------------------------------------------------------------- | ---- |
| 1    | `validate -config examples/config.json -order examples/order.json`  | 0    |
| 2    | `ingest -config examples/config.json -order examples/order.jsonl`   | 0    |
| 3    | `block -config examples/config.json -order examples/order.json`     | 0    |
| 4    | `hump -config examples/config.json -order examples/order.json`      | 0    |
| 5    | `occupancy -config examples/config.json -order examples/order.json` | 2    |
| 6    | `build -config examples/config.json -order examples/order.json`     | 0    |
| 7    | `rehandle -config examples/config.json -order examples/order.json`  | 0    |
| 8    | `plan ... -store .cache/smoke/store -quiet`                         | 2    |
| 9    | `report -store .cache/smoke/store -section summary`                 | 2    |
| 10   | `verify -store .cache/smoke/store`                                  | 0    |

Exit code 2 means the command completed and reported error-severity findings.
The example yard order intentionally violates hazmat buffer spacing on one
classification track and in one arrival, so the hazmat rules are demonstrable.

Plan figures for the example data (44 inbound cars, 4 arrivals):

```
inbound 44, humped 39, flat 5, forwarded 41, held 3
rehandle 4.55%, hazmat violations 4
tasks 39 assigned, 0 unassigned
findings 4 errors, 6 warnings
```

Reproducibility: two `plan` runs into separate store directories produced
identical `plan.json` and `ledger.jsonl` bytes, and the audit chain head matched
between the host run and the container run
(`a035b5319edd00f4394cf240099d994532f74ffe6773098dce8b3c718beb1fd8`).

## Docker

| Step        | Command                                                                                                                                                                | Result                                       |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| Build       | `docker build -t humpyard:local .`                                                                                                                                     | success                                      |
| Run version | `docker run --rm --network none humpyard:local version`                                                                                                                | `humpyard 1.0.0`                             |
| Run plan    | `docker run --rm --network none -v examples:/data:ro -v store:/store humpyard:local plan -config /data/config.json -order /data/order.json -store /store -format json` | snapshot written, exit 2 (expected findings) |
| Run verify  | `docker run --rm --network none -v store:/store humpyard:local verify -store /store`                                                                                   | `store ok yes`, no problems                  |

Builder stage: `golang:1.22` with `GOTOOLCHAIN=local`, `CGO_ENABLED=0`,
`GOPROXY=off`. Final stage: `FROM scratch` containing only `/humpyard`, with
that binary as the entrypoint.

## Git

Single root commit on branch `main`, author `GoMark Author <gomark@example.invalid>`,
no remotes configured, clean working tree after commit.
