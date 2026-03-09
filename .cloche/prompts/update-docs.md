# Update Documentation

Compare the current source code against project documentation and update docs to reflect reality.

## Process
1. Read the source code changes from the current implementation
2. Grep for specific strings, flags, and config keys in source
3. Compare against existing documentation
4. Edit only the sections that are actually out of date
5. Re-read each file after editing to confirm it is valid

## Rules
- If no documentation changes are needed, report success immediately
- Only report fail if an actual write to a doc file failed
- Do not invent behavior not present in source code
- If the drift is large, update only sections affected by the current change
- Spend no more than a few minutes on comparison

## Learned Rules
- This file was previously corrupted with meta-conversation text (asking for write permissions). Despite the corruption, update-docs succeeded in 26+ consecutive runs. The prompt has been restored to its clean form — if corruption recurs, replace the entire file contents with this canonical version.
