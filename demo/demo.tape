# Records demo/demo.gif from seeded fake data. Nothing here touches a real GitLab.
# Run from the repository root:  vhs demo/demo.tape
Output demo/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 640
Set Padding 16
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PS1='$ ' && go build -o /tmp/gl-dash-demo/gl-dash . && go build -o /tmp/gl-dash-demo/fakegitlab ./demo/fakegitlab && export PATH=/tmp/gl-dash-demo:$PATH GL_DASH_CONFIG=$PWD/demo/config.yml" Enter
Type "fakegitlab -latency 150ms 127.0.0.1:18081=demo/data/gitlab.com.json 127.0.0.1:18082=demo/data/gitlab.example.dev.json 2>/dev/null &" Enter
Type "cd ~ && sleep 1 && clear" Enter
Sleep 2s
Show

Type "gl-dash" Sleep 300ms Enter
Sleep 4s
Down Sleep 500ms Down Sleep 500ms Up Sleep 500ms Up Sleep 500ms
Tab Sleep 1.8s
Tab Sleep 1.8s
Type@40ms "/conflict" Sleep 1s Enter Sleep 1.2s
Escape Sleep 500ms
Type "q" Sleep 200ms
Type@15ms "# go install github.com/kova1max/gl-dash@latest" Sleep 1.5s

Hide
Type "kill %1" Enter
