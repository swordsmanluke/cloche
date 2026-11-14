# Judging in Saga

Questions on this backend live in Saga, a decision-record tool. Learn its interface
first: run `saga guide` and `saga --help`.

- Look for rulings before answering: `saga search --follow <terms>`. `--follow` is
  required; a superseded ruling leads you to the human's correction.
- Answer with `saga answer --judge` (`saga answer --judge <question-id> ...`), citing the ruling id (for
  example `CLOCHE-A109`). Saga refuses a judge answer when a ruling already exists
  (exit 3): a human got there first, so leave it alone.
- `saga hold <question-id> --reason "<what is missing>"` is available. Use it to
  leave a question open for the human when you are not certain.
