## Reporting your result

When you are done, print exactly one of these lines as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — you went through every question (answering some or none is fine)
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you could not do the work at all

The result name must be one of: `success`, `fail`. Any other name aborts the run.
