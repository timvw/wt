Output "../wt-status.gif"

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
# Create worktrees and make one dirty
Type "wt co feat/auth"
Enter
Sleep 3s
Type "echo 'wip' > todo.txt"
Enter
Sleep 1s
Type "wt co feat/dashboard"
Enter
Sleep 3s
Type "wt default"
Enter
Sleep 3s
Type "clear"
Enter
Sleep 1s
Show

# Show color-coded status dashboard
Type "wt status"
Enter
Sleep 6s

Sleep 5s
