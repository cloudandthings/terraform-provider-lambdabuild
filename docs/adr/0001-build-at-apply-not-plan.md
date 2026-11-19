# Build Artifacts at apply, not at plan

Plan only computes the Input fingerprint; the zip is built during apply. We chose this so that a saved plan created on one runner applies cleanly on another, plans stay fast and need no package-index access, and a non-reproducible dependency surfaces as a clear "non-reproducible build" error rather than Terraform's fatal "Provider produced inconsistent final plan". The cost is that `output_base64sha256` is unknown at plan when inputs change; reviewers still see that code changed because `output_path` (derived from the fingerprint) changes.

## Consequences: saved plans and changed sources

Terraform re-plans every resource at apply and compares the result with the saved plan; the provider never sees the saved plan itself. So when sources change between `terraform plan -out` and `terraform apply`, the re-plan computes a different Input fingerprint and Terraform itself stops the apply with its own error, `Provider produced inconsistent final plan ... .input_fingerprint: was "...", but now "..."`, plus a note asking to report a provider bug. That note is noise: the apply failed safely, nothing was built, and the fix is to plan again. We accept this (decided after ticket 14) rather than work around Terraform's consistency check. The provider's own "Build inputs changed since plan" error covers only the remaining window: sources changing after Terraform's re-plan, just before or during the build. The provider recomputes the fingerprint before it builds, and removes anything a build of changed sources wrote, so no Artifact of unreviewed sources is left behind.

A saved plan that plans no change to a resource builds nothing at apply. Applied on a runner that does not have the Artifact (for example a fresh CI runner), the zip stays absent there. Lambda resources that only read `output_path` and hashes from state are unaffected, but anything in the same apply that reads the zip file itself (an `aws_s3_object` upload, a `filebase64sha256()` call) fails. Keep the default `on_missing_artifact = "rebuild"` on runners that apply, so the plan made on a runner without the Artifact plans its rebuild.

## Considered Options

- **Plan-time data source (like `archive_file`)**: rejected; the zip exists only on the plan runner, so saved plans break on ephemeral CI.
- **Always build at plan, rebuild and verify at apply**: rejected for v1; every PR plan does a full install with network access, unknown inputs need a second code path anyway, and any byte drift is fatal.
- **Opt-in `build_at_plan`**: on the roadmap for teams that want hashes known at plan.
