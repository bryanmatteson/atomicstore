# Run scripts/record-readme-demo.py from the repository root.
Output docs/assets/demo.gif
Set Shell "bash"
Set FontSize 20
Set Width 1280
Set Height 700
Set Padding 26
Set Framerate 10
Set TypingSpeed 30ms
Set Theme "Catppuccin Mocha"
Hide
Type@0ms `alias atomicstore-demo='"$README_DEMO_BIN"'; export PS1='atomicstore ❯ '; unset PROMPT_COMMAND; clear`
Enter
Show
Type@0ms "# Real filesystem storage: stale writes, batches, and leases"
Enter
Type "atomicstore-demo"
Enter
Sleep 18s
