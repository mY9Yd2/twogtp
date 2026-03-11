BUILD_DIR := build

CHECK_DUPES_BINARY_NAME := check_dupes
CHECK_DUPES_SRC := check_dupes.go

TWOGTP_BINARY_NAME := twogtp
TWOGTP_SRC := twogtp.go

.PHONY: all build-check_dupes build-twogtp clean

all: build-check_dupes build-twogtp

build-twogtp:
	@echo "Building $(TWOGTP_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(TWOGTP_BINARY_NAME) $(TWOGTP_SRC)
	@echo "Build complete: $(BUILD_DIR)/$(TWOGTP_BINARY_NAME)"

build-check_dupes:
	@echo "Building $(CHECK_DUPES_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(CHECK_DUPES_BINARY_NAME) $(CHECK_DUPES_SRC)
	@echo "Build complete: $(BUILD_DIR)/$(CHECK_DUPES_BINARY_NAME)"

clean:
	@echo "Cleaning up..."
	@go clean
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"
