# The three things I actually run. Everything else is go test ./...

.PHONY: build test bench clean

build:
	go build -o contractcheck ./cmd/contractcheck

test:
	go test -race ./...

# -benchtime 10x because the fixture is built once per iteration and 1s
# worth of iterations spends a minute building fixtures.
bench:
	go test ./internal/protodiff/ -run XXX -bench . -benchtime 10x

clean:
	rm -f contractcheck
