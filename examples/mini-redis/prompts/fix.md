Fix the C code based on the validation failures below.
Only modify files that need fixing. Do not rewrite the entire project.

Focus on:
1. Compiler errors and warnings first (must compile with -Wall -Wextra -Werror -pedantic)
2. Test failures second (read the assertion output carefully)
3. Memory errors from valgrind (leaks, use-after-free, invalid reads/writes)
4. Static analysis warnings from cppcheck

Read the error output carefully. Fix the root cause, not symptoms.
When fixing valgrind errors, trace the allocation back to its source
and ensure every code path frees it.
