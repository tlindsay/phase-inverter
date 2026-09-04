SHELL := bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

# ifeq ($(origin .RECIPEPREFIX), undefined)
#   $(error This Make does not support .RECIPEPREFIX. Please use GNU Make 4.0 or later)
# endif
# .RECIPEPREFIX = >

APP_PATH_EXECUTABLE ?= ./Phase\ Inverter.app/Contents/MacOS/
BUILD_DIR ?= ./build
EXE_NAME ?= phase_inverter
BUILD_TARGET := $(BUILD_DIR)/$(EXE_NAME)

DAEMON_NAME ?= pattern_enhancer
DAEMON_TARGET := $(BUILD_DIR)/$(DAEMON_NAME)

# Where the daemon listens when run locally, and which receiver it drives.
RECEIVER ?= 192.168.1.101
LOCAL_ADDR ?= 127.0.0.1:8642
DEVICE ?= office

.PHONY: app
app: build
	cp $(BUILD_TARGET) $(APP_PATH_EXECUTABLE)

.PHONY: build
build:
	go build -o $(BUILD_TARGET) ./cmd/phase-inverter

.PHONY: daemon
daemon:
	go build -o $(DAEMON_TARGET) ./cmd/pattern-enhancer

.PHONY: all
all: build daemon

# Run the daemon against the real receiver without joining the tailnet. Needs
# to be on the same LAN as the receiver, since MusicCast's event push is a UDP
# datagram back to whatever address made the request.
.PHONY: run-daemon
run-daemon: daemon
	$(DAEMON_TARGET) -v -receiver $(RECEIVER) -device $(DEVICE) -local $(LOCAL_ADDR)

# Run the menubar app against a locally running daemon rather than the tailnet.
.PHONY: run-app
run-app: build
	$(BUILD_TARGET) -daemon http://$(LOCAL_ADDR) -device $(DEVICE)

.PHONY: test
test:
	go test -race ./...
