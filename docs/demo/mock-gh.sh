#!/bin/bash
# Mock gh CLI for demo recordings.
# Handles the subset of commands that wt pr uses.

case "$*" in
  "pr list --json number,title --jq "*)
    # Interactive PR list
    printf "42\tAdd user authentication\n"
    printf "37\tFix dashboard layout\n"
    printf "29\tUpdate API rate limiting\n"
    ;;
  "pr view 42 --json headRefName")
    echo '{"headRefName":"feat/auth"}'
    ;;
  "pr view 37 --json headRefName")
    echo '{"headRefName":"fix/login-bug"}'
    ;;
  "pr view 29 --json headRefName")
    echo '{"headRefName":"feat/dashboard"}'
    ;;
  *)
    echo "mock-gh: unhandled: $*" >&2
    exit 1
    ;;
esac
