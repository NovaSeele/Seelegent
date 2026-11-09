package guard

import "strings"

// Code runners write files WriteTargets cannot name: an interpreter running a script or inline code, a package
// manager's script, a build tool.
var (
	runnerNames = set("python", "python3", "python2", "py", "pythonw", "node", "nodejs", "deno", "bun", "bunx",
		"ruby", "perl", "php", "lua", "rscript", "julia", "java", "groovy", "kotlin", "scala", "tsx", "ts-node",
		"esno", "vite-node", "npm", "pnpm", "yarn", "npx", "pnpx", "make", "gmake", "nmake", "mingw32-make",
		"cmake", "ninja", "cargo", "dotnet", "gradle", "gradlew", "mvn", "mvnw", "ant", "rake", "bundle",
		"composer", "poetry", "uv", "uvx", "pipenv", "tox", "nox", "just", "task", "cscript", "wscript",
		"source", "invoke-expression", "iex", "start-process", "saps", "start")
	// goWriting are the go subcommands that run a program.
	goWriting = set("run", "generate", "test")
	// scriptExts are the extensions of a script started by its path (./gen.sh, .\fix.ps1).
	scriptExts = []string{".sh", ".bash", ".ps1", ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".bat", ".cmd"}
)

// Runs reports whether a command line runs a program that may write files its words do not name: an interpreter
// (python fix.py, node -e ...), a package manager or build tool (npm run format, make, go run ./tools/x, cargo run),
// a shell running a script file, or a script started by its path. The line is read in every dialect that can read
// it; an unreadable line counts as running one.
func Runs(line string) bool {
	read := false
	for _, sh := range allShells {
		st, err := parseState(line, sh, 0)
		if err != nil {
			continue
		}
		read = true
		for _, seg := range st.out {
			if segmentRuns(seg.words, 0) {
				return true
			}
		}
	}
	return !read
}

func segmentRuns(ws []word, depth int) bool {
	for len(ws) > 0 && (ws[0].assign || !ws[0].quoted && keywords[ws[0].text]) {
		ws = ws[1:]
	}
	if len(ws) > 1 && !ws[0].quoted && (ws[0].text == "&" || ws[0].text == ".") {
		ws = ws[1:] // PowerShell's call operator, a POSIX source
	}
	if len(ws) == 0 || depth > writeDepth {
		return false
	}
	first := strings.Trim(ws[0].text, `"'`)
	name, args := cmdName(ws[0]), ws[1:]
	switch {
	case strings.HasPrefix(first, "$") && len(args) > 0 && args[0].text == "=":
		return segmentRuns(args[1:], depth) // a PowerShell assignment: $x = <command>
	case strings.HasPrefix(first, "$") && strings.Contains(first, "="):
		return segmentRuns(args, depth) // $x=1, or $x=<command> written without spaces
	case ws[0].dynamic:
		return true // a program a variable names
	case prefixVerbs[name]:
		for len(args) > 0 && !args[0].quoted && (strings.HasPrefix(args[0].text, "-") ||
			assignment.MatchString(args[0].text)) {
			args = args[1:]
		}
		return segmentRuns(args, depth)
	case runnerNames[name]:
		return true
	case name == "go":
		return len(args) > 0 && goWriting[strings.ToLower(args[0].text)]
	case name == "bash" || name == "sh" || name == "zsh" || name == "dash":
		if code, ok := flagValue(args, "-c"); ok {
			return lineRuns(code, POSIX, depth+1)
		}
		return len(positionalWords(args, POSIX)) > 0 // a script file
	case name == "cmd":
		for i, a := range args {
			if t := strings.ToLower(a.text); t == "/c" || t == "/k" {
				return lineRuns(joinWords(args[i+1:]), Cmd, depth+1)
			}
		}
		return false
	case name == "powershell" || name == "pwsh":
		for _, f := range []string{"-c", "-command"} {
			if code, ok := flagValue(args, f); ok {
				return lineRuns(code, PowerShell, depth+1)
			}
		}
		return len(args) > 0 // -File, or a script
	}
	if strings.ContainsAny(first, `/\`) {
		return true // a program or script started by its path
	}
	lower := strings.ToLower(first)
	for _, ext := range scriptExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func lineRuns(line string, sh Shell, depth int) bool {
	st, err := parseState(line, sh, 0)
	if err != nil {
		return true
	}
	for _, seg := range st.out {
		if segmentRuns(seg.words, depth) {
			return true
		}
	}
	return false
}
