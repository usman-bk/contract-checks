# contract-checks

_Built with AI assistance._

A pull request job that fails when a change would break an app already on
somebody's phone.

Two app releases are live at any time and nobody can force an update, so the
question on every API change is not whether it compiles. It is whether a
client built against last month's contract still works against the server
that is about to ship. Catching that by eye worked until the number of
endpoints passed 180 and three squads were touching them in the same week.

Go 1.23, no services, no network.

```
internal/protodiff/   compile both sides, compare the descriptors
internal/sqldiff/     read CREATE TABLE, compare the columns
internal/report/      findings, levels, and three output formats
cmd/contractcheck/    the command CI runs
```

## It compares descriptors, not text

A text diff of a .proto reports a reordered field and a reformatted comment,
and misses the change that matters: a field whose type came from an imported
file that somebody else edited. The descriptor is what the client and the
server actually agree on, so both sides are compiled and the compiled form is
compared.

A proto that does not compile fails the job. It does not pass it, and it does
not pass it quietly, which is what happens when a tool treats a parse error as
"no changes found".

## What counts as breaking

| Rule | Why a shipped client cares |
|---|---|
| `field-removed` | it stops arriving, and reserving the number does not give it back |
| `field-renamed` | the wire number survives, the JSON name does not |
| `field-type-changed` | the old client decodes the value wrongly or not at all |
| `field-cardinality-changed` | singular and repeated are not the same wire shape |
| `field-number-reused` | an old client's bytes are read as a different field |
| `field-oneof-changed` | setting it now clears its siblings |
| `message-removed`, `enum-removed`, `service-removed` | something still refers to it |
| `enum-value-removed`, `enum-value-renamed` | a client may still send it |
| `rpc-removed` | a shipped app still calls it |
| `rpc-input-changed`, `rpc-output-changed`, `rpc-streaming-changed` | a different call |

Adding a field, a message, an enum value or an endpoint is not breaking, and
the test that says so is the one that decides whether anybody keeps the job
switched on. A tool that fails on a new field gets deleted in a fortnight.

Removing a field is reported even when the number is correctly reserved.
Reserving is the right thing to do and it prevents the worse bug, which is the
number coming back meaning something else. It does not stop a client losing
the field.

### Risky, which reports and does not fail

Adding an enum value is the whole category. Nothing breaks at the wire level,
and a client built before it lands receives a number it has no name for, so
somebody should check what the app does with an unknown value. Failing the job
for it would teach the team to pass the flag that turns the entire check off,
which costs more than the warning is worth.

## The SQL half

A deploy is not atomic. For some minutes the old version and the new one are
both live against the same database, so a migration has to work in both
directions: the old code must survive the new schema, and the new code must
survive the old one. That is stricter than "the migration applies cleanly".

| Breaking | Why |
|---|---|
| `table-dropped`, `column-dropped` | the old version is still selecting it while the deploy rolls |
| `column-added-not-null` with no default | the old version's inserts know nothing about it and start failing |
| `column-made-not-null` with no default | the same, from the other direction |
| `column-type-changed`, narrowing | existing values may not fit |

Narrowing is distinguished from widening, because calling every type change
breaking is advice nobody can act on. `bigint` to `integer` fails, `integer`
to `bigint` passes. `varchar(64)` to `varchar(32)` fails, `varchar(32)` to
`text` passes. Spellings Postgres treats as one type compare equal, so
rewriting `timestamptz` as `timestamp with time zone` reports nothing.

The parser handles the subset the schema is written in and returns an error on
anything else, rather than skipping it. A silently skipped table is a check
that passes by looking at nothing.

## A run that checked nothing is a failure

`contractcheck` exits non zero if it found no messages and no services.

This is the most likely way for the job to stop working: somebody moves the
proto directory, the flag still points at the old path, the tool finds an
empty tree and reports no incompatible changes, and the check is green and
useless for months. Every run also prints what it examined.

    checked 9 messages, 26 fields, 1 enums, 1 services, 4 methods
    no incompatible changes

## It never touches the working tree

The old side comes from `git archive`, which reads the object database and
writes into a temporary directory. Not a second clone, which is the slowest
thing this tool could do on every pull request, and not a stash or a checkout,
which leave the runner, or somebody's laptop, in a state they did not ask for
when the job is cancelled halfway through. One case runs the whole thing
against a real repository and then asserts `git status` is clean.

The comparison is against the **merge base**, not the tip of the main branch.
Comparing against the tip reports somebody else's breaking change as yours,
which is the other way a team learns to ignore a job.

## Using it

    contractcheck -proto proto -sql migrations

    contractcheck -base-dir old/proto -head-dir new/proto    # compare two directories
    contractcheck -base v1.4.0                               # compare against a tag
    contractcheck -format github                             # annotations on the pull request
    contractcheck -format json                               # for another tool

| Exit | Meaning |
|---|---|
| 0 | compatible |
| 1 | at least one breaking change |
| 2 | the tool was used wrongly, including a run that checked nothing |
| 3 | something failed, such as a proto that does not compile |

An example of the output it is worth reading:

    BREAKING  field-removed  .marketplace.v1.Listing.in_stock: field removed,
              number correctly reserved, and a client that reads it still loses it
            was: bool in_stock = 7
            now: absent

## Speed

A contract of 18 services, 180 endpoints, 360 messages and 1,440 fields is
compiled on both sides and compared in **10.5 ms**, measured by
`BenchmarkLoadAndDiff180Endpoints`. The command including process start is
about 30 ms.

    go test ./internal/protodiff/ -run XXX -bench . -benchtime 10x

The benchmark builds that contract rather than committing it, so the number is
reproducible and a change that makes the comparison quadratic shows up here
instead of in a pull request that times out.

## The test suite

    go test ./...     41 cases, no services needed

`TestAgainstARealGitRepository` creates a repository, commits a contract,
branches, breaks it, and runs the command the way CI does, through the merge
base and `git archive`.

Two bugs in the SQL parser came out of its own tests. `varchar(50)` to `text`
was reported as a narrowing because the two types have different names, and a
quoted column name containing a space was split down the middle, so
`"odd name" numeric(10,2)` parsed as a column called `odd`. Both are in the
history.

## What it cannot catch

It does not know which fields a client actually reads. Removing a field nobody
ever used is reported exactly like removing one every screen depends on,
because the proto cannot tell them apart and guessing would be worse.

It does not check HTTP or JSON schemas, only protobuf and SQL.

It cannot see a semantic break. Changing the meaning of a status code, or
starting to return prices excluding tax, passes every rule here and breaks
every client.
