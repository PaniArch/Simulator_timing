# Experiment logs

This tracked directory is retained only as documentation and is not the default
runtime log destination. With `env/env.sh` active, `support/logging` writes
append-only text logs under `$SIMULATOR_LOG_DIR`: the Harness invocation runtime
or `.cache/logs` for a manually activated shell. Callers may still select another
directory explicitly through the logging package API.
