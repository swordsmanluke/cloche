# Asking in Saga

Questions become Saga questions in one batch under the bind's `SAGA_PROJECT` and the
question file's `backend.feature` (set it, for example `{"backend":{"feature":"cloche/qa-backends"}}`).

- Ask one decision per question, with 2-4 options and one marked recommended.
- Put what a reader needs in `context` and `detail`: Saga rulings outlive the run.
- The answer comes back as the ruling in force; a human's answer beats a judge's.
