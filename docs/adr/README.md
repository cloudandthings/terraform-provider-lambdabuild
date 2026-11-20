# Architecture decision records

Each record captures one significant decision: what was decided, why, its consequences, and the options rejected. Read the ones touching your area before changing it. If a change contradicts a record, say so in the PR and update the record or add a new one that supersedes it.

| # | Decision |
| --- | --- |
| [0001](0001-build-at-apply-not-plan.md) | Build Artifacts at apply, not at plan |
| [0002](0002-embed-uv-as-subprocess.md) | Embed uv in the provider binary and run it as a subprocess |
| [0003](0003-uv-version-not-in-fingerprint.md) | Leave the uv version out of the Input fingerprint |

## Writing a new record

Name it `NNNN-short-title.md` with the next number. Keep it short: a title stating the decision, a paragraph on the decision and its reasons, then **Consequences** and **Considered Options**. Use the terms in [GLOSSARY.md](../../GLOSSARY.md). Add it to the table above and to the [architecture overview](../architecture/README.md#decision-records).
