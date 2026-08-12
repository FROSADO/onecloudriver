.PHONY: build test test-unit test-integration test-all clean lint lint-all lint-security setup-fuse security-audit dist deb rpm release release-check docs help
# ──── Variables ────
BINARY     := onecloudriver
CMD_DIR    := ./cmd/onecloudriver
FS_PKG     := ./internal/fs/...
GORACE     := GORACE="log_path=.race strip_path_prefix=1"
GOTEST     := go test
RACE_FLAGS := -race

# ──── Version ────
# git describe returns the closest tag (e.g. v0.1.0).
# We strip the 'v' prefix for dpkg which requires letter-free versions.
# Examples: v0.1.0 → 0.1.0, v0.1.0-3-gbf99c4f → 0.1.0-3-gbf99c4f
_RAW_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
ifeq ($(_RAW_VERSION),)
  VERSION := 0.1.0
else
  VERSION := $(_RAW_VERSION)
endif
ARCH         := $(shell go env GOARCH 2>/dev/null || echo "amd64")
OS           := $(shell go env GOOS 2>/dev/null || echo "linux")
DIST_NAME    := $(BINARY)_$(OS)_$(ARCH)
DEB_NAME     := $(BINARY)_$(VERSION)_$(ARCH)

# ──── Systemd user service template ────
# Shared by the deb and rpm targets so the unit can never diverge between
# packages. Specifiers are written DOUBLED (%%h, %%i): the value is emitted
# through `printf '$(SERVICE_UNIT)'`, and printf collapses %% → %, so the
# generated file must end up with SINGLE %h/%i for systemd to expand them
# (%h = user home, %i = instance name; a literal %% is an escaped percent,
# see systemd.unit(5)). Do NOT write single %h/%i here — printf would
# reject %h and would turn a lone %i into a numeric conversion.
SERVICE_UNIT := [Unit]\nDescription=OneCloudRiver - OneDrive Filesystem\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=/usr/local/bin/onecloudriver mount %%h/OneDrive/%%i -a %%i\nExecStop=/bin/fusermount3 -uz %%h/OneDrive/%%i\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n

# ──── RPM Version handling ────
# RPM does not allow hyphens in the version string, so we separate the version and release.
# if VERSION is "0.1.0-3-gbf99c4f", we split it into:
#   - Version: 0.1.0
#   - Release: 3.gbf99c4f (hyphens converted to dots)
# if git describe returns "v0.1.0-3-gbf99c4f", we split it into:
#   - Version: 0.1.0
#   - Release: 3.gbf99c4f (hyphens converted to dots)
# If only a clean tag is available (v0.1.0), release is "1".

_BASE_VERSION := $(shell echo $(VERSION) | sed 's/-.*//')
RPM_VERSION := $(_BASE_VERSION)

_GIT_SUFFIX := $(shell echo $(VERSION) | sed -n 's/^[^-]*-//p')
ifeq ($(_GIT_SUFFIX),)
    RPM_RELEASE := 1
else
    RPM_RELEASE := $(shell echo $(_GIT_SUFFIX) | tr '-' '.')
endif


# ──── RPM Architecture mapping ────
# Go usa nombres de arquitectura diferentes a RPM.
# Mapeamos GOARCH → arquitectura RPM estándar.

ifeq ($(ARCH),amd64)
    RPM_ARCH := x86_64
else ifeq ($(ARCH),386)
    RPM_ARCH := i686
else ifeq ($(ARCH),arm64)
    RPM_ARCH := aarch64
else ifeq ($(ARCH),arm)
    RPM_ARCH := armv7hl
else ifeq ($(ARCH),ppc64le)
    RPM_ARCH := ppc64le
else ifeq ($(ARCH),s390x)
    RPM_ARCH := s390x
else ifeq ($(ARCH),riscv64)
    RPM_ARCH := riscv64
else
    RPM_ARCH := $(ARCH)
endif

# Nombre final del archivo RPM
RPM_NAME := $(BINARY)-$(RPM_VERSION)-$(RPM_RELEASE).$(RPM_ARCH)

RPMBUILD_DIR := $(PWD)/rpmbuild

# Use bash to have pipefail
SHELL := /bin/bash

# ──── Build ────

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) $(CMD_DIR)

build-race:
	go build -race -ldflags "-X main.version=$(VERSION)" -o $(BINARY) $(CMD_DIR)

# ──── Setup ────

setup-fuse:
	@bash scripts/setup-fuse.sh

# ──── Tests ────
#
# ⚠️  IMPORTANT ORDER: Integration tests (real FUSE) MUST always
# run AFTER the unit/mock tests. Running both in parallel
# can cause interference (orphaned FUSE mounts, conflicting ports).
#
# ✅ Correct:    make test-all          (unit → integration)
# ✅ Correct:    make test-unit && make test-integration
# ❌ Incorrect:  go test ./... & go test -tags=integration ./... &

# Unit tests: all tests that do NOT require a FUSE mount.
# Excludes files with the "integration" build tag.
test-unit:
	@set -o pipefail; \
	$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 -v $(FS_PKG) 2>&1 | \
		grep --color=always -E '(=== RUN|--- PASS|--- FAIL|--- SKIP|^ok|^FAIL|^panic)' || true

# Only the final result (for CI)
test-unit-short:
	$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 $(FS_PKG)

# Integration tests: require a mounted FUSE.
# Verifies the environment first with setup-fuse.sh (uses its exit code).
# Uses -tags=integration to include files with the build tag.
test-integration:
	@if ! bash scripts/setup-fuse.sh > /dev/null; then \
		echo "⚠️  Integration tests skipped (FUSE environment not available)"; \
		exit 0; \
	fi; \
	set -o pipefail; \
	$(GORACE) $(GOTEST) $(RACE_FLAGS) -covermode atomic -coverprofile=covprofile  -count=1 -v -tags=integration $(FS_PKG) 2>&1 | \
		grep --color=always -E '(=== RUN|--- PASS|--- FAIL|--- SKIP|^ok|^FAIL|^panic)' || true

# Only the final result (for CI)
test-integration-short:
	@if ! bash scripts/setup-fuse.sh > /dev/null; then \
		echo "SKIP: FUSE environment not available"; \
		exit 0; \
	fi; \
	$(GORACE) $(GOTEST) $(RACE_FLAGS) -covermode atomic -coverprofile=covprofile  -count=1 -tags=integration $(FS_PKG) 

# All tests: unit + integration.
# Uses -run '^TestIntegration' for the integration pass to avoid
# running the unit tests twice.
test-all:
	@echo "=== Unit tests (mock) ==="
	$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 $(FS_PKG)
	@echo ""
	@sleep 0.5
	@if ! bash scripts/setup-fuse.sh > /dev/null; then \
		echo "=== Integration tests: SKIP (FUSE environment not available) ==="; \
	else \
		echo "=== Integration tests ==="; \
		$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 -tags=integration -run '^TestIntegration' $(FS_PKG); \
	fi
	@echo ""
	@echo "All tests completed."

# ──── Lint ────

# Standard lint: uses .golangci.yml (style + bugs + basic security).
lint:
	golangci-lint run ./internal/fs/...

lint-all:
	golangci-lint run ./...

# Security lint: uses .golangci-security.yml (security linters only).
lint-security:
	golangci-lint run -c .golangci-security.yml ./internal/... ./cmd/...

# ──── Coverage ────

coverage:
	go test -coverprofile=coverage.out $(FS_PKG)
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage saved to coverage.html"

# ──── Cleanup ────

clean:
	@echo "Cleaning build and test artifacts..."
	rm -f $(BINARY)
	rm -f coverage.out coverage.html
	rm -f .race.*
	rm -f audit-report.txt
	@rm -rf $(PWD)/rpmbuild
	@rm -f *.rpm
	find . -name '__debug_bin*' -delete 2>/dev/null || true
	# Clean up orphaned FUSE mounts (just in case)
	@for mp in $$(mount | grep 'onecloudriver' | awk '{print $$3}' 2>/dev/null); do \
		echo "Unmounting $$mp..."; \
		fusermount3 -uz "$$mp" 2>/dev/null || fusermount -uz "$$mp" 2>/dev/null || true; \
	done
	@echo "Cleanup completed."

# ──── Security Audit ────

# security-audit: Runs all security tools and generates a consolidated
# report in audit-report.txt.
#
# Tools executed:
#   1. gosec        — SAST: static analysis of vulnerabilities in code
#   2. govulncheck  — CVE scan of dependencies
#   3. golangci-lint — Security linters (gosec, bodyclose, errcheck, etc.)
#   4. go test -race — Data race detector
#
# The report includes a final summary with a count of findings by severity
# and a global PASS/FAIL verdict.
#
# Requirements: gosec, govulncheck, golangci-lint installed.
#   go install github.com/securego/gosec/v2/cmd/gosec@latest
#   go install golang.org/x/vuln/cmd/govulncheck@latest
#   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
security-audit:
	@rm -f audit-report.txt /tmp/gosec_audit.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  SECURITY AUDIT — OneCloudRiver" | tee -a audit-report.txt
	@echo "  Date: $$(date '+%Y-%m-%d %H:%M:%S')" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "🔍 Checking tools..." | tee -a audit-report.txt
	@FAIL=0; \
	for tool in gosec govulncheck golangci-lint; do \
		if ! command -v $$tool &>/dev/null; then \
			echo "  ❌ $$tool: NOT FOUND" | tee -a audit-report.txt; \
			FAIL=1; \
		else \
			echo "  ✅ $$tool: $$(command -v $$tool)" | tee -a audit-report.txt; \
		fi; \
	done; \
	if [ $$FAIL -eq 1 ]; then \
		echo "" | tee -a audit-report.txt; \
		echo "❌ Missing tools. Install them with:" | tee -a audit-report.txt; \
		echo "  go install github.com/securego/gosec/v2/cmd/gosec@latest" | tee -a audit-report.txt; \
		echo "  go install golang.org/x/vuln/cmd/govulncheck@latest" | tee -a audit-report.txt; \
		echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" | tee -a audit-report.txt; \
		exit 1; \
	fi
	@echo "" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  1/4 — gosec (SAST)" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "Scanning code for vulnerabilities..." | tee -a audit-report.txt
	@gosec -quiet ./... 2>&1 | tee /tmp/gosec_audit.txt | tee -a audit-report.txt; \
	GOSEC_EXIT=$${PIPESTATUS[0]}; \
	echo "" | tee -a audit-report.txt; \
	if [ $$GOSEC_EXIT -eq 0 ]; then \
		echo "✅ gosec: no findings" | tee -a audit-report.txt; \
	else \
		echo "⚠️  gosec: findings found (see count below)" | tee -a audit-report.txt; \
	fi
	@echo "" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  2/4 — govulncheck (dependency CVEs)" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "Scanning dependencies for known CVEs..." | tee -a audit-report.txt
	@if govulncheck ./... 2>&1 | tee -a audit-report.txt; then \
		echo "" | tee -a audit-report.txt; \
		echo "✅ govulncheck: no vulnerabilities found" | tee -a audit-report.txt; \
	else \
		echo "" | tee -a audit-report.txt; \
		echo "❌ govulncheck: vulnerabilities found (see above)" | tee -a audit-report.txt; \
	fi
	@echo "" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  3/4 — golangci-lint (security linters)" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "Running security linters (config: .golangci-security.yml)..." | tee -a audit-report.txt
	@if golangci-lint run -c .golangci-security.yml ./internal/... ./cmd/... 2>&1 | tee -a audit-report.txt; then \
		echo "" | tee -a audit-report.txt; \
		echo "✅ golangci-lint: no security findings" | tee -a audit-report.txt; \
	else \
		echo "" | tee -a audit-report.txt; \
		echo "⚠️  golangci-lint: findings found (see above)" | tee -a audit-report.txt; \
	fi
	@echo "" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  4/4 — go test -race (data races)" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "Running unit tests (mock) with the race detector..." | tee -a audit-report.txt
	@$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 $(FS_PKG) 2>&1 | tee -a audit-report.txt; \
	UNIT_EXIT=$$?; \
	echo "" | tee -a audit-report.txt; \
	echo "Running integration tests (real FUSE)..." | tee -a audit-report.txt; \
	if bash scripts/setup-fuse.sh > /dev/null 2>&1; then \
		$(GORACE) $(GOTEST) $(RACE_FLAGS) -count=1 -tags=integration $(FS_PKG) 2>&1 | tee -a audit-report.txt; \
		INT_EXIT=$$?; \
	else \
		echo "SKIP: FUSE environment not available" | tee -a audit-report.txt; \
		INT_EXIT=0; \
	fi; \
	if [ $$UNIT_EXIT -eq 0 ] && [ $$INT_EXIT -eq 0 ]; then \
		echo "" | tee -a audit-report.txt; \
		echo "✅ go test -race: no data races" | tee -a audit-report.txt; \
	else \
		echo "" | tee -a audit-report.txt; \
		echo "❌ go test -race: data races detected (see above)" | tee -a audit-report.txt; \
	fi
	@echo "" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "  FINAL SUMMARY" | tee -a audit-report.txt
	@echo "========================================" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "Tools executed:" | tee -a audit-report.txt
	@echo "  1. gosec        — SAST (static analysis)" | tee -a audit-report.txt
	@echo "  2. govulncheck  — dependency CVEs" | tee -a audit-report.txt
	@echo "  3. golangci-lint — Security linters" | tee -a audit-report.txt
	@echo "  4. go test -race — Data race detector" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@echo "─── gosec findings by severity ───" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt
	@HIGH=$$(grep 'Severity: HIGH' /tmp/gosec_audit.txt 2>/dev/null | wc -l | tr -d ' '); \
	MED=$$(grep 'Severity: MEDIUM' /tmp/gosec_audit.txt 2>/dev/null | wc -l | tr -d ' '); \
	LOW=$$(grep 'Severity: LOW' /tmp/gosec_audit.txt 2>/dev/null | wc -l | tr -d ' '); \
	TOTAL=$$(grep 'Severity:' /tmp/gosec_audit.txt 2>/dev/null | wc -l | tr -d ' '); \
	echo "  🔴 HIGH   : $$HIGH" | tee -a audit-report.txt; \
	echo "  🟡 MEDIUM : $$MED" | tee -a audit-report.txt; \
	echo "  🟢 LOW    : $$LOW" | tee -a audit-report.txt; \
	echo "  📊 TOTAL  : $$TOTAL" | tee -a audit-report.txt; \
	echo "" | tee -a audit-report.txt; \
	if [ "$$HIGH" -gt 0 ]; then \
		echo "⚠️  There are $$HIGH HIGH-severity findings — they require attention." | tee -a audit-report.txt; \
	elif [ "$$MED" -gt 0 ]; then \
		echo "⚠️  There are $$MED MEDIUM-severity findings — review in the current sprint." | tee -a audit-report.txt; \
	elif [ "$$TOTAL" -gt 0 ]; then \
		echo "ℹ️  Only LOW findings ($$TOTAL). No immediate critical risks." | tee -a audit-report.txt; \
	else \
		echo "✅ Zero security findings — excellent!" | tee -a audit-report.txt; \
	fi; \
	rm -f /tmp/gosec_audit.txt
	@echo "" | tee -a audit-report.txt
	@echo "📄 Full report saved to: audit-report.txt" | tee -a audit-report.txt
	@echo "" | tee -a audit-report.txt

# ──── Distribution ────

# dist: Generates a zip with the compiled binary and the user manual.
# The zip is created in the project root directory.
#
#    make dist
#    unzip onecloudriver_linux_amd64.zip
#    sudo cp onecloudriver /usr/local/bin/
dist: build
	@echo "📦 Generating distribution package..."
	@rm -rf /tmp/$(DIST_NAME) /tmp/$(DIST_NAME).zip
	@mkdir -p /tmp/$(DIST_NAME)
	@cp $(BINARY) /tmp/$(DIST_NAME)/
	@cp docs/MANUAL.md /tmp/$(DIST_NAME)/README.md
	@cp docs/onecloudriver.1 /tmp/$(DIST_NAME)/
	@cp docs/onecloudriver.1.es /tmp/$(DIST_NAME)/onecloudriver.1.es
	@chmod +x /tmp/$(DIST_NAME)/$(BINARY)
	@cd /tmp && zip -r $(DIST_NAME).zip $(DIST_NAME) > /dev/null
	@mv /tmp/$(DIST_NAME).zip .
	@rm -rf /tmp/$(DIST_NAME)
	@echo "✅ $(DIST_NAME).zip generated ($(shell du -h $(DIST_NAME).zip | cut -f1))"
	@echo "   Contents:"
	@unzip -l $(DIST_NAME).zip | tail -n +4 | head -n -2

deb: build
	@echo "📦 Generating .deb package..."
	@if ! command -v dpkg-deb &>/dev/null; then \
		echo "❌ dpkg-deb not found. Install dpkg."; \
		exit 1; \
	fi
	@rm -rf /tmp/deb-pkg
	@mkdir -p /tmp/deb-pkg/DEBIAN
	@mkdir -p /tmp/deb-pkg/usr/local/bin
	@mkdir -p /tmp/deb-pkg/usr/share/man/man1
	@mkdir -p /tmp/deb-pkg/usr/share/man/es/man1
	@mkdir -p /tmp/deb-pkg/usr/share/doc/$(BINARY)
	@mkdir -p /tmp/deb-pkg/usr/lib/systemd/user
	@# Binary
	@cp $(BINARY) /tmp/deb-pkg/usr/local/bin/
	@# Man pages (English default + Spanish localized; man selects by locale)
	@gzip -c docs/onecloudriver.1 > /tmp/deb-pkg/usr/share/man/man1/$(BINARY).1.gz
	@gzip -c docs/onecloudriver.1.es > /tmp/deb-pkg/usr/share/man/es/man1/$(BINARY).1.gz
	@# Documentation
	@cp docs/MANUAL.md /tmp/deb-pkg/usr/share/doc/$(BINARY)/README.md
	@# Systemd user service template (same path as the .rpm: /usr/lib/systemd/user)
	@printf '$(SERVICE_UNIT)' > /tmp/deb-pkg/usr/lib/systemd/user/$(BINARY)@.service
	@# Sanity check: the unit must contain single %i/%h specifiers. A leftover
	@# '%%i' means the escaping regressed and the instance never expands.
	@grep -q '[^%]%i' /tmp/deb-pkg/usr/lib/systemd/user/$(BINARY)@.service || { echo "❌ unit missing %i instance specifier"; exit 1; }
	@if grep -q '%%i' /tmp/deb-pkg/usr/lib/systemd/user/$(BINARY)@.service; then echo "❌ unit contains escaped '%%i' (instance specifier broken)"; exit 1; fi
	@if command -v systemd-analyze &>/dev/null; then systemd-analyze verify /tmp/deb-pkg/usr/lib/systemd/user/$(BINARY)@.service || echo "⚠️  systemd-analyze verify reported issues (see above)"; fi
	@# DEBIAN control
	@echo "Package: $(BINARY)" > /tmp/deb-pkg/DEBIAN/control
	@echo "Version: $(VERSION)" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Section: utils" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Priority: optional" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Architecture: $(ARCH)" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Depends: fuse3" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Maintainer: OneCloudRiver <dev@onecloudriver.local>" >> /tmp/deb-pkg/DEBIAN/control
	@echo "Description: Native OneDrive filesystem for Linux" >> /tmp/deb-pkg/DEBIAN/control
	@echo " OneCloudRiver mounts your OneDrive as a FUSE filesystem," >> /tmp/deb-pkg/DEBIAN/control
	@echo " allowing you to read, write, create, and delete files" >> /tmp/deb-pkg/DEBIAN/control
	@echo " directly from your file manager and terminal." >> /tmp/deb-pkg/DEBIAN/control
	@chmod -R 755 /tmp/deb-pkg/DEBIAN
	@dpkg-deb --build /tmp/deb-pkg $(DEB_NAME).deb > /dev/null
	@rm -rf /tmp/deb-pkg
	@echo "✅ $(DEB_NAME).deb generated ($(shell du -h $(DEB_NAME).deb | cut -f1))"
	@echo ""
	@echo "   To install:"
	@echo "     sudo dpkg -i $(DEB_NAME).deb"
	@echo ""
	@echo "   To view the manual:"
	@echo "     man $(BINARY)"
	@echo ""
	@echo "   Package contents:"
	@dpkg-deb -c $(DEB_NAME).deb

# ──── RPM Package ────

# rpm: Generates an .rpm package for Fedora, RHEL, CentOS, Rocky, AlmaLinux.
# Requires: rpm-build (sudo dnf install rpm-build)
#
# make rpm
# sudo dnf install onecloudriver-0.1.0-1.x86_64.rpm
#
# The package includes:
#   - Binary in /usr/local/bin/
#   - Man pages (English + Spanish) in /usr/share/man/
#   - Documentation in /usr/share/doc/onecloudriver/
#   - Systemd user service template in /usr/lib/systemd/user/
rpm: build
	@echo "📦 Generating .rpm package..."
	@if ! command -v rpmbuild &> /dev/null; then \
		echo "❌ rpmbuild not found. Install rpm-build:"; \
		echo "   Fedora/RHEL: sudo dnf install rpm-build"; \
		echo "   openSUSE:    sudo zypper install rpm-build"; \
		echo "   Arch:        sudo pacman -S rpm-tools"; \
		echo "   Debian/Ubuntu: sudo apt-get install rpm"; \
		exit 1; \
	fi
	@echo "   Version: $(RPM_VERSION)"
	@echo "   Release: $(RPM_RELEASE)"
	@echo "   Arch:    $(RPM_ARCH)"
	@echo ""
	@# Clean up any previous rpmbuild directory
	@rm -rf $(RPMBUILD_DIR)
	@mkdir -p $(RPMBUILD_DIR)/{BUILD,RPMS,SOURCES,SPECS,SRPMS,BUILDROOT}
	@# Copy binary and assets to SOURCES
	@cp $(BINARY) $(RPMBUILD_DIR)/SOURCES/
	@gzip -c docs/onecloudriver.1 > $(RPMBUILD_DIR)/SOURCES/$(BINARY).1.gz
	@gzip -c docs/onecloudriver.1.es > $(RPMBUILD_DIR)/SOURCES/$(BINARY).1.es.gz
	@cp docs/MANUAL.md $(RPMBUILD_DIR)/SOURCES/README.md
	@# Copy LICENSE if it exists (create a placeholder if not)
	@if [ -f LICENSE ]; then \
		cp LICENSE $(RPMBUILD_DIR)/SOURCES/LICENSE; \
	else \
		echo "GPLv3 License - See https://github.com/FROSADO/onecloudriver" > $(RPMBUILD_DIR)/SOURCES/LICENSE; \
	fi
	@# Generate systemd user service
	@printf '$(SERVICE_UNIT)' > $(RPMBUILD_DIR)/SOURCES/$(BINARY)@.service
	@# Sanity check: the unit must contain single %i/%h specifiers. A leftover
	@# '%%i' means the escaping regressed and the instance never expands.
	@grep -q '[^%]%i' $(RPMBUILD_DIR)/SOURCES/$(BINARY)@.service || { echo "❌ unit missing %i instance specifier"; exit 1; }
	@if grep -q '%%i' $(RPMBUILD_DIR)/SOURCES/$(BINARY)@.service; then echo "❌ unit contains escaped '%%i' (instance specifier broken)"; exit 1; fi
	@if command -v systemd-analyze &>/dev/null; then systemd-analyze verify $(RPMBUILD_DIR)/SOURCES/$(BINARY)@.service || echo "⚠️  systemd-analyze verify reported issues (see above)"; fi
	@# Generate the .spec file
	@echo "Name:           $(BINARY)" > $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Version:        $(RPM_VERSION)" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Release:        $(RPM_RELEASE)%{?dist}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Summary:        Native OneDrive filesystem for Linux" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "License:        GPLv3" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "URL:            https://github.com/FROSADO/onecloudriver" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source0:        %{name}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source1:        %{name}.1.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source2:        %{name}.1.es.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source3:        README.md" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source4:        %{name}@.service" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Source5:        LICENSE" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Requires:       fuse3" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%description" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "OneCloudRiver mounts your OneDrive as a FUSE filesystem," >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "allowing you to read, write, create, and delete files" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "directly from your file manager and terminal." >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "Unlike sync clients, it performs on-demand downloads" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "when your computer attempts to use the files." >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%install" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/local/bin" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/share/man/man1" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/share/man/es/man1" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/share/doc/%{name}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/share/licenses/%{name}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "mkdir -p %{buildroot}/usr/lib/systemd/user" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 755 %{SOURCE0} %{buildroot}/usr/local/bin/%{name}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 644 %{SOURCE1} %{buildroot}/usr/share/man/man1/%{name}.1.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 644 %{SOURCE2} %{buildroot}/usr/share/man/es/man1/%{name}.1.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 644 %{SOURCE3} %{buildroot}/usr/share/doc/%{name}/README.md" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 644 %{SOURCE4} %{buildroot}/usr/lib/systemd/user/%{name}@.service" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "install -m 644 %{SOURCE5} %{buildroot}/usr/share/licenses/%{name}/LICENSE" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%files" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%license /usr/share/licenses/%{name}/LICENSE" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "/usr/local/bin/%{name}" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "/usr/share/man/man1/%{name}.1.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "/usr/share/man/es/man1/%{name}.1.gz" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%doc /usr/share/doc/%{name}/README.md" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "/usr/lib/systemd/user/%{name}@.service" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "%changelog" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "* $$(LC_ALL=C date '+%a %b %d %Y') OneCloudRiver <dev@onecloudriver.local> - $(RPM_VERSION)-$(RPM_RELEASE)" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@echo "- Package generated from git commit $(shell git rev-parse --short HEAD 2>/dev/null || echo 'unknown')" >> $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@# Build the RPM
	@echo ""
	@echo "🔨 Building RPM..."
	@rpmbuild --quiet --define "_topdir $(RPMBUILD_DIR)" -bb $(RPMBUILD_DIR)/SPECS/$(BINARY).spec
	@# Move the generated RPM to the project root (usando RPM_ARCH correcto)
	@mv $(RPMBUILD_DIR)/RPMS/$(RPM_ARCH)/$(RPM_NAME).rpm .
	@# Cleanup
	@rm -rf $(RPMBUILD_DIR)
	@echo ""
	@echo "✅ $(RPM_NAME).rpm generated ($$(du -h $(RPM_NAME).rpm | cut -f1))"
	@echo ""
	@echo " To install:"
	@echo "   sudo dnf install ./$(RPM_NAME).rpm"
	@echo ""
	@echo " To view the manual:"
	@echo "   man $(BINARY)"
	@echo ""
	@echo " Package contents:"
	@rpm -qpl $(RPM_NAME).rpm

# ──── Release ────

# release: Automates the GitHub release process (scripts/release.sh).
# Interactive flow: pre-flight checks → version → CHANGELOG draft →
# commit + tag vX.Y.Z → push (triggers the Release workflow) → monitoring.
# Requires: gh CLI authenticated (gh auth login).
release:
	@bash scripts/release.sh

# release-check: Pre-flight checklist for a new release (read-only).
release-check:
	@bash scripts/release.sh --check

# ──── Documentation ────

# docs: Generates docs/api/ with the API documentation extracted
# directly from the godoc comments of the source code.
#
# Uses go doc -all for each public package of the project.
# The output is in text format (compatible with Markdown).
# Requires no external tools — only go.
#
#    make docs && cat docs/api/fs.md
docs:
	@echo "📚 Generating API documentation from godoc..."
	@mkdir -p docs/api
	@echo '# API: internal/fs' > docs/api/fs.md
	@echo '' >> docs/api/fs.md
	@echo '> Auto-generated with `go doc -all`. Date: '$$(date '+%Y-%m-%d %H:%M:%S') >> docs/api/fs.md
	@echo '' >> docs/api/fs.md
	@echo '```' >> docs/api/fs.md
	@go doc -all ./internal/fs >> docs/api/fs.md 2>&1
	@echo '```' >> docs/api/fs.md
	@echo '# API: internal/auth' > docs/api/auth.md
	@echo '' >> docs/api/auth.md
	@echo '> Auto-generated with `go doc -all`. Date: '$$(date '+%Y-%m-%d %H:%M:%S') >> docs/api/auth.md
	@echo '' >> docs/api/auth.md
	@echo '```' >> docs/api/auth.md
	@go doc -all ./internal/auth >> docs/api/auth.md 2>&1
	@echo '```' >> docs/api/auth.md
	@echo '# API: internal/graph' > docs/api/graph.md
	@echo '' >> docs/api/graph.md
	@echo '> Auto-generated with `go doc -all`. Date: '$$(date '+%Y-%m-%d %H:%M:%S') >> docs/api/graph.md
	@echo '' >> docs/api/graph.md
	@echo '```' >> docs/api/graph.md
	@go doc -all ./internal/graph >> docs/api/graph.md 2>&1
	@echo '```' >> docs/api/graph.md
	@echo '# API: cmd/onecloudriver' > docs/api/cmd.md
	@echo '' >> docs/api/cmd.md
	@echo '> Auto-generated with `go doc -all`. Date: '$$(date '+%Y-%m-%d %H:%M:%S') >> docs/api/cmd.md
	@echo '' >> docs/api/cmd.md
	@echo '```' >> docs/api/cmd.md
	@go doc -all ./cmd/onecloudriver >> docs/api/cmd.md 2>&1
	@echo '```' >> docs/api/cmd.md
	@echo "✅ Documentation generated in docs/api/"
	@echo "   $$(wc -l docs/api/*.md 2>/dev/null | tail -1 | awk '{print $$1}') total lines"
	@echo ""
	@echo "   To view:"
	@echo "     cat docs/api/fs.md"
	@echo "     cat docs/api/auth.md"
	@echo "     cat docs/api/graph.md"
	@echo "     cat docs/api/cmd.md"

# ──── Help ────

help:
	@echo "Available targets:"
	@echo ""
	@echo "  build                 Build the binary"
	@echo "  build-race            Build with race detector"
	@echo "  setup-fuse            Verify FUSE prerequisites"
	@echo "  test-unit             Unit tests (no FUSE, verbose + grep)"
	@echo "  test-unit-short       Only final unit result (CI)"
	@echo "  test-integration      Integration tests (with FUSE mounted)"
	@echo "  test-integration-short Only final integration result (CI)"
	@echo "  test-all              Unit + integration"
	@echo "  lint                  Lint only internal/fs (uses .golangci.yml)"
	@echo "  lint-all              Lint the whole project (uses .golangci.yml)"
	@echo "  lint-security         Security lint (uses .golangci-security.yml)"
	@echo "  coverage              Test coverage"
	@echo "  security-audit        Complete security audit → audit-report.txt"
	@echo "  dist                  Generate distribution zip (binary + manual)"
	@echo "  deb                   Generate .deb package (binary + man page)"
	@echo "  rpm                   Generate .rpm package (binary + man page)"
	@echo "  release               Interactive release automation (scripts/release.sh)"
	@echo "  release-check         Pre-flight checklist for a new release (read-only)"
	@echo "  docs                  Generate docs/api/ with go doc -all"
	@echo "  clean                 Clean artifacts"
	@echo "  help                  This help"
	@echo ""
	@echo "Example of a full workflow:"
	@echo "  make setup-fuse && make test-all"
	@echo ""
	@echo "CI example (correct order: unit → integration):"
	@echo "  make clean && make build && make test-unit-short && make test-integration-short"
	@echo ""
	@echo "⚠️  NEVER run unit and integration tests in parallel."
	@echo ""
	@echo "Distribution:"
	@echo "  make dist && unzip onecloudriver_linux_amd64.zip"
	@echo "  make deb  && sudo dpkg -i onecloudriver_*.deb"
	@echo "  make rpm && sudo dnf install onecloudriver-*.rpm"
	@echo ""
	@echo "Security audit:"
	@echo "  make security-audit && cat audit-report.txt"
