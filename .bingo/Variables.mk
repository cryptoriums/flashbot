# Auto generated binary variables helper managed by https://github.com/bwplotka/bingo v0.7. DO NOT EDIT.
# All tools are designed to be build inside $GOBIN.
BINGO_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GOPATH ?= $(shell go env GOPATH)
GOBIN  ?= $(firstword $(subst :, ,${GOPATH}))/bin
GO     ?= $(shell which go)

# Below generated variables ensure that every time a tool under each variable is invoked, the correct version
# will be used; reinstalling only if needed.
# For example for faillint variable:
#
# In your main Makefile (for non array binaries):
#
#include .bingo/Variables.mk # Assuming -dir was set to .bingo .
#
#command: $(FAILLINT)
#	@echo "Running faillint"
#	@$(FAILLINT) <flags/args..>
#
FAILLINT := $(GOBIN)/faillint-v1.9.0
$(FAILLINT): $(BINGO_DIR)/faillint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/faillint-v1.9.0"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=faillint.mod -o=$(GOBIN)/faillint-v1.9.0 "github.com/fatih/faillint"

GOLANGCI_LINT := $(GOBIN)/golangci-lint-v1.50.1-0.20221015115940-a760cb1239a9
$(GOLANGCI_LINT): $(BINGO_DIR)/golangci-lint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/golangci-lint-v1.50.1-0.20221015115940-a760cb1239a9"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=golangci-lint.mod -o=$(GOBIN)/golangci-lint-v1.50.1-0.20221015115940-a760cb1239a9 "github.com/golangci/golangci-lint/cmd/golangci-lint"

