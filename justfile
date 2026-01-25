# Binary name and build directory
binary_name := "wt"
build_dir := "bin"

# Show available recipes
default:
    @just --list

# Build the binary
build:
    mkdir -p {{build_dir}}
    go build -o {{build_dir}}/{{binary_name}} .

# Install to /usr/local/bin (requires sudo)
install: build
    sudo cp {{build_dir}}/{{binary_name}} /usr/local/bin/

# Install to ~/bin (no sudo required)
install-user: build
    mkdir -p ~/bin
    cp {{build_dir}}/{{binary_name}} ~/bin/
    @echo "Make sure ~/bin is in your PATH"

# Clean build artifacts
clean:
    go clean
    rm -rf {{build_dir}}

# Run unit tests
test:
    go test -v -short ./...

# Run e2e tests with all available shells
e2e: build
    go run e2e/run.go --wt={{build_dir}}/{{binary_name}} --verbose

# Run e2e tests with specific shells (comma-separated)
e2e-shells shells: build
    go run e2e/run.go --wt={{build_dir}}/{{binary_name}} --shells={{shells}} --verbose

# Run e2e tests with bash only
e2e-bash: build
    go run e2e/run.go --wt={{build_dir}}/{{binary_name}} --shells=bash --verbose

# Run e2e tests with zsh only
e2e-zsh: build
    go run e2e/run.go --wt={{build_dir}}/{{binary_name}} --shells=zsh --verbose

# Run all tests (unit + e2e)
test-all: test e2e

# Cross-compile for multiple platforms
build-all:
    mkdir -p {{build_dir}}
    GOOS=linux GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-linux-amd64 .
    GOOS=darwin GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-darwin-amd64 .
    GOOS=darwin GOARCH=arm64 go build -o {{build_dir}}/{{binary_name}}-darwin-arm64 .
    GOOS=windows GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-windows-amd64.exe .
