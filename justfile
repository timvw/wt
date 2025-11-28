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

# Run tests
test:
    go test -v ./...

# Cross-compile for multiple platforms
build-all:
    mkdir -p {{build_dir}}
    GOOS=linux GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-linux-amd64 .
    GOOS=darwin GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-darwin-amd64 .
    GOOS=darwin GOARCH=arm64 go build -o {{build_dir}}/{{binary_name}}-darwin-arm64 .
    GOOS=windows GOARCH=amd64 go build -o {{build_dir}}/{{binary_name}}-windows-amd64.exe .
