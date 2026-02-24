Fix the Go code based on the validation failures below.
Only modify files that need fixing. Do not rewrite unrelated code.

## Guidelines
- Read the error output carefully — fix the root cause, not symptoms
- If tests fail, check both the test and the implementation
- If `go vet` fails, fix the specific issues it reports
- If build fails, check for missing imports, type errors, or syntax issues
- Run `go test ./...` after fixing to verify
