# Mock AWS playground

Plan and apply the provider from this checkout against a mock AWS
([moto server](https://docs.getmoto.org/en/latest/docs/server_mode.html) in
Docker). Nothing reaches real AWS and no credentials are needed.

The configuration builds `./app` (a uv project) three ways and deploys two
Lambda functions:

- `lambdabuild-mock`: a code-only function Artifact plus a dependencies layer.
- `lambdabuild-mock-bundled`: one function Artifact with code and dependencies, no layer.

It also creates the IAM role and an S3 bucket holding the three SBOMs.

## Use

```sh
mise run mock-aws:up        # start moto on localhost:5050
mise run mock-aws:plan
mise run mock-aws:apply     # extra args go to terraform, e.g. -- -auto-approve
mise run mock-aws:tf -- output
mise run mock-aws:destroy
mise run mock-aws:down      # stop moto, delete state and build/
```

Every terraform task rebuilds the provider into `dist/mock-aws/` and loads it
through `dev_overrides`, so code changes are picked up on the next run.
Terraform warns about the override; that is expected.

Environment:

- `MOCK_AWS_PORT`: host port for moto (default 5050; 5000 is taken by AirPlay on macOS).
- `TF_BIN=tofu`: run the same configuration with OpenTofu.

moto keeps everything in memory. Restarting it loses the resources, so run
`mock-aws:down` instead of only stopping the container, or the state goes stale.

## Things to try

- **Code change:** edit `app/handler.py`. The `code` and `bundled` Artifacts and both functions change; the layer does not.
- **Dependency change:** bump `requests` in `app/pyproject.toml`, run `uv lock` in `app/`. The layer and the `bundled` Artifact change; the `code` Artifact does not.
- **Architecture:** `-- -var architecture=x86_64`.
- **Fresh runner:** delete `build/`. A plan with the default `on_missing_artifact = "rebuild"` rebuilds the Artifacts and leaves the AWS resources unchanged (same bytes). `-- -var drift_check=true` plans no changes.
- **Saved plan:** `-- -out=tfplan`, edit `app/handler.py`, then `mise run mock-aws:tf -- apply tfplan` fails with Terraform's `Provider produced inconsistent final plan`: the expected, safe failure for sources changed since plan (see ADR 0001).

The mock stores what it is given but does not run functions: invoking them
needs real Lambda or `mise run test:smoke`.
