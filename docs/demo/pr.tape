Output "../wt-pr.gif"

Set Theme "Dracula"
Set FontSize 16
Set Width 1200
Set Height 600
Set TypingSpeed 30ms
Set Padding 20

Hide
Type "docker run --rm -it -e WORKTREE_ROOT=/home/demo/worktrees wt-demo bash --rcfile /home/demo/.bashrc"
Enter
Sleep 3s
Type "cd src/main-app"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 1s
Show

# Checkout a PR by number
Type "wt pr 42"
Enter
Sleep 5s

# Navigate back to main
Type "wt default"
Enter
Sleep 3s

# Show all worktrees including the PR one
Type "wt status"
Enter
Sleep 6s

Sleep 5s
