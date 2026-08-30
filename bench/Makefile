# Bench targets for the onecloudriver performance comparison.
#
# Builds the baseline (v0.1.4 tag) and current binaries into bench/bin, then
# runs the reproduced battery and writes ../docs/PERFORMANCE_REPORT.md.

BIN_DIR  := bin
BASELINE := $(BIN_DIR)/onecloudriver-v014
CURRENT  := $(BIN_DIR)/onecloudriver-current
V014WT   := /tmp/ocr-v014

.PHONY: bins baseline bin current run compare clean help

## bins          build both binaries (baseline v0.1.4 tag + current HEAD)
bins: baseline bin

## baseline      build only the v0.1.4 baseline binary
baseline: $(BASELINE)

## bin           build only the current binary
bin: $(CURRENT)

$(CURRENT):
	go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty | sed 's/^v//')" -o "$@" ./cmd/onecloudriver

$(BASELINE):
	@if [ ! -d "$(V014WT)" ]; then echo ">> crear worktree de v0.1.4"; git worktree add -q "$(V014WT)" v0.1.4; fi
	cd "$(V014WT)" && go build -ldflags "-X main.version=0.1.4" -o "$(shell pwd)/$(BASELINE)" ./cmd/onecloudriver

## run           poll results live
run:
	./run_benchmark.sh --baseline && ./run_benchmark.sh --current

## compare       run both batteries and generate the report
compare:
	./run_benchmark.sh --compare

## clean         remove generated sample dirs (keeps binaries)
clean:
	rm -rf .run

# --- FUSE cache battery (issue #66) --------------------------------------
## fuse-baseline  measure baseline cache battery on the live FUSE mount
fuse-baseline:
	./run_fuse.sh baseline

## fuse-current   measure current cache battery on the live FUSE mount
fuse-current:
	./run_fuse.sh current

## fuse-compare   run both cache batteries and render docs/PERFORMANCE_REPORT.md
fuse-compare:
	./run_fuse.sh compare

## fuse-report    re-render the report from existing results/*.json only
fuse-report:
	./run_fuse.sh report

## topclean      remove binaries and the v0.1.4 worktree too
topclean: clean
	rm -f $(BASELINE) $(CURRENT)
	git worktree remove --force "$(V014WT)" 2>/dev/null || true

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'