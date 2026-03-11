BUILD_DIR := build

BINARY_NAME := twogtp

.PHONY: all build clean

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) .
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

clean:
	@echo "Cleaning up..."
	@go clean
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"
