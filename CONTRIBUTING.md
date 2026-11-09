# Contributing

If the tool reports a change as breaking and it is not, that is the bug I most
want to hear about. A false positive on a pull request is how a team learns to
ignore the job, which is worse than not having the job.

Open an issue with the two descriptors, or the two bits of SQL, and what you
expected. A failing test case in `internal/protodiff/diff_test.go` or
`internal/sqldiff/diff_test.go` is better still: both read as a table of
before, after and verdict, so a new row is usually three lines.

Before sending a change:

    make test
    gofmt -l .

Two rules I will hold a change to. A new rule needs a case in both directions,
the break and the safe equivalent, because a rule that only fires is a rule
nobody can trust. And exit codes do not move: 0, 1, 2 and 3 are in the README
and somebody has them in a shell script.
