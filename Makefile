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

.PHONY: app
app: build
	cp $(BUILD_TARGET) $(APP_PATH_EXECUTABLE)

.PHONY: build
build:
	go build -o $(BUILD_TARGET)
