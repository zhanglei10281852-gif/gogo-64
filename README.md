# HumpYard

HumpYard is an offline command line planner for railway hump yard (classification
yard) car marshalling. It reads a yard configuration and a yard order, then works
out how the arriving cars should be blocked, humped, stored in the bowl, pulled
into outbound trains and covered by crews, and it records the result in a local
hash-chained store.

Everything runs locally. The program makes no network calls, uses only the Go
standard library, and depends on no third-party modules.

## Independence notice

This is an independent, original project. It is not affiliated with,
endorsed by, sponsored by, or associated with any other product, company,
organization, railway, or standards body. Any resemblance between the sample
reporting marks, yard names or destination codes used in the examples and real
entities is incidental; the sample data is fictional and exists only to
exercise the planner.

## What it does

| Stage     | Description                                                                                                                      |
| --------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Ingest    | Strict JSON or JSONL decoding of yard orders, cross-checked against the configuration                                            |
| Blocking  | Destination to block mapping, block to classification track assignment under length, weight, restriction and placard limits      |
| Hump      | Cut sequencing at the crest, with flat-switch handling for cars that cannot be humped, retarder settings and rider requirements  |
| Occupancy | Replay of every movement onto the bowl, remaining length and weight per track, overflow detection, final standing order          |
| Hazmat    | Buffer-car spacing between incompatible classes, prohibition near occupied caboose and crew positions, per-track placard ceiling |
| Build     | Outbound consists pulled block by block under train length, weight and axle ceilings and locomotive tonnage ratings              |
| Rehandle  | Misroutes, second hump passes, holds, and the derived rehandle percentage                                                        |
| Shift     | Task derivation and crew assignment with duty-hour limits                                                                        |
| Store     | Append-only work ledger, atomic plan snapshot, metadata, SHA-256 hash-chained audit log with verification                        |

## Build

```
go build ./...
go test ./... -count=1
go vet ./...
gofmt -l .
```

The module targets `go 1.22.5`. Builds and tests are expected to run with
`GOTOOLCHAIN=local` and `GOPROXY=off`.

To produce the binary:

```
go build -o .cache/bin/humpyard ./cmd/humpyard
```

## Usage

```
humpyard <command> [flags]
```

| Command     | Purpose                                                            |
| ----------- | ------------------------------------------------------------------ |
| `validate`  | Check a configuration, and optionally a yard order against it      |
| `ingest`    | Decode a yard order, summarize it, optionally record it in a store |
| `block`     | Print the blocking plan and per-track loading                      |
| `hump`      | Print the crest sequence: cuts, flat moves and timings             |
| `occupancy` | Simulate the bowl and validate hazmat placement                    |
| `build`     | Assemble outbound trains and print consist manifests               |
| `rehandle`  | Print rework items and the rehandle percentage                     |
| `plan`      | Run the whole chain, print the full plan, persist a snapshot       |
| `report`    | Render a stored snapshot, its ledger, or its audit chain           |
| `verify`    | Recompute the audit chain and cross-check the store                |

Common flags:

| Flag                 | Meaning                                              |
| -------------------- | ---------------------------------------------------- |
| `-config path`       | Configuration JSON document                          |
| `-order path`        | Yard order document, `.json` or `.jsonl`             |
| `-store dir`         | Local store directory, created on demand             |
| `-format text\|json` | Output format, default `text`                        |
| `-quiet`             | Suppress informational text output                   |
| `-section`           | `report` only: `all`, `summary`, `ledger` or `audit` |

Exit codes:

| Code | Meaning                                                         |
| ---- | --------------------------------------------------------------- |
| 0    | Success, no error findings                                      |
| 1    | Usage problem, unreadable input, or invalid configuration       |
| 2    | The command ran, but the result carries error-severity findings |

Exit code 2 is normal for the bundled example data: it deliberately contains
hazmat spacing violations so that the validation rules are visible.

### Example session

```
humpyard validate  -config examples/config.json -order examples/order.json
humpyard ingest    -config examples/config.json -order examples/order.jsonl
humpyard block     -config examples/config.json -order examples/order.json
humpyard hump      -config examples/config.json -order examples/order.json
humpyard occupancy -config examples/config.json -order examples/order.json
humpyard build     -config examples/config.json -order examples/order.json
humpyard rehandle  -config examples/config.json -order examples/order.json
humpyard plan      -config examples/config.json -order examples/order.json -store .cache/store
humpyard report    -store .cache/store -section summary
humpyard verify    -store .cache/store
```

## Input formats

### Configuration

A single strict JSON document. Unknown members are rejected and no content may
follow the top-level object. See `examples/config.json` for a complete document.
The sections are:

- `yard`: identity, crest grade, long-car threshold, coupler slack, overflow and
  repair track references.
- `blocks`: block identifier, name, priority (unique, lower is pulled first) and
  the destination codes it covers.
- `classification_tracks`: bowl tracks with capacity in feet, weight limit in
  tons, grade, restrictions (`no-hazmat`, `no-placard`, `no-rough-rider`,
  `no-long-car`, `flat-only`, `no-loaded`), an optional caboose spot and a
  retarder identifier.
- `receiving_tracks` and `departure_tracks`: support tracks with capacity,
  weight limit and lead time in minutes.
- `locomotives`: length, weight, axles, tonnage rating, horsepower and whether
  the unit is restricted to yard service.
- `crews`: qualifications (`hump`, `flat`, `rider`, `road-train`, `inspect`),
  duty-minute limit and home shift.
- `shifts`: start minute, duration, crest car capacity and rider count.
  Shifts must not overlap.
- `hazmat_rules`: declared classes, incompatible pairs with required buffer-car
  counts, classes barred next to an occupied caboose or crew position, classes
  that may never be humped, the per-track placard ceiling and the caboose
  buffer size.
- `hump_rules`: cut size limit, maximum humpable car length, retarder weight
  capacity, rider policy, minimum bowl grade, cut change time, crest rate and
  flat switching time per car.
- `departure_orders`: outbound trains with the block pull order, length, weight
  and axle ceilings, assigned power, departure minute and minimum car count.

### Yard order

Two interchangeable forms, both strictly decoded:

Whole document (`.json`):

```json
{
  "order_id": "WBKH-2201",
  "yard_id": "WBKH",
  "trains": [
    {
      "id": "T101",
      "arrival_minute": 30,
      "receiving_track": "R01",
      "inspected": true,
      "caboose_position": 12,
      "cars": [
        {
          "mark": "BNSF",
          "number": "471203",
          "kind": "boxcar",
          "length_ft": 60,
          "tare_tons": 30.5,
          "gross_tons": 110.0,
          "axles": 4,
          "destination": "ALB"
        }
      ]
    }
  ]
}
```

Stream (`.jsonl` or `.ndjson`): one record per line, blank lines and lines
beginning with `#` ignored. The first record may be an order header; every other
record is a train. A `record` tag is mandatory:

```
{"record":"order","order_id":"WBKH-2202","yard_id":"WBKH"}
{"record":"train","id":"T201","arrival_minute":60,"receiving_track":"R01","cars":[ ... ]}
```

Cars are listed in standing order from the leading end. A car may carry a
`hazmat_class` with a `placard` flag, a `restriction` (`free`, `no-hump`,
`flat-only`, `cushion`, `no-uncouple`), a `drawbar_mate` for permanently
coupled pairs, `easy_roller` or `rough_rider` handling flags, and a
`bad_order` flag with a mandatory `bad_order_why` reason.

All times are integer minutes on a rolling clock; text output renders them as
`day+hh:mm`.

## Store layout

A store is a plain directory:

| File           | Content                                                              |
| -------------- | -------------------------------------------------------------------- |
| `meta.json`    | Format version, identity, counters, audit head hash, snapshot digest |
| `ledger.jsonl` | Append-only work ledger, one record per line, sequentially numbered  |
| `plan.json`    | Latest plan snapshot, written atomically                             |
| `audit.jsonl`  | Hash-chained audit log, one record per line                          |

Each audit record hashes its own fields together with the previous record hash,
so editing any record breaks that record and every link after it. `verify`
recomputes the chain, compares the head with the metadata, checks the ledger
sequence and re-digests the snapshot.

Whole-file writes go to a temporary file in the same directory, are flushed, and
are then renamed over the target, so a reader never sees a half-written file.

## Determinism

The same inputs always produce byte-identical output:

- Every collection is sorted on load: tracks, blocks, crews, shifts, cars and
  findings all have a defined total order.
- Maps are never iterated directly for output; keys are sorted first.
- Tie-breaks are explicit. Track selection prefers the most remaining length,
  then the lexically smallest track identifier. Crew selection prefers the least
  loaded crew, then the smallest crew identifier. Findings sort by severity,
  scope, subject and message.
- No wall-clock time, process identifier, random value or file-system ordering
  ever reaches stored output. All timing is derived from the integer minute
  fields in the input.
- Stored JSON is encoded with a fixed indent and HTML escaping disabled; ledger
  and audit lines are compact single-line JSON.

Running `plan` twice into two different store directories yields identical
`plan.json` and `ledger.jsonl` bytes, which the test suite asserts.

## Docker

Multi-stage build: the builder stage compiles a static binary with the pinned
toolchain and the module proxy disabled; the final stage is `scratch` and
contains only that binary.

```
docker build -t humpyard:local .
docker run --rm --network none humpyard:local version
```

Mount data in and a store out, still with networking disabled:

```
docker run --rm --network none \
  -v "$PWD/examples:/data:ro" \
  -v "$PWD/.cache/docker-store:/store" \
  humpyard:local plan -config /data/config.json -order /data/order.json -store /store

docker run --rm --network none \
  -v "$PWD/.cache/docker-store:/store" \
  humpyard:local verify -store /store
```

## Repository layout

```
cmd/humpyard        command entry point
internal/jsonx      strict JSON and JSONL decoding, canonical encoding
internal/model      domain types: cars, tracks, power, crews, shifts, orders
internal/config     configuration document, loader and semantic validation
internal/ingest     yard order loading, statistics and cross-checks
internal/blocking   destination to block and block to track assignment
internal/hump       crest sequencing, cuts and flat moves
internal/hazmat     hazardous material placement validation
internal/occupancy  bowl occupancy simulation
internal/depart     outbound train building and manifests
internal/rehandle   rework detection and rehandle percentage
internal/shift      task derivation and crew assignment
internal/store      ledger, snapshot, metadata and hash-chained audit log
internal/pipeline   stage orchestration and snapshot persistence
internal/report     deterministic text and JSON rendering
internal/cli        subcommand dispatch and flag handling
examples            sample configuration and yard orders
```

Local build, test and smoke artifacts are written under `.cache/`, which is
ignored by git and excluded from the Docker build context.
