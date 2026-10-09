# Evaluation corpus contract

Versioned fixtures and result records for ProtoBot requirements and
Job Site evaluation. This directory is the executable contract for
issue #215. It is not the Agent Eval Harness skill corpus under
`eval/eliciting-requirements/`.

## Layout

- `schema/` — JSON Schema for fixtures, holdout assets, projections,
  and results.
- `examples/` — Illustrative CLI, network-service, and web-gui
  fixtures plus sample result records.
- `validate.py` — Deterministic validator. No hosted cluster, Git
  host, or external service credentials.

## Local validation

From the repository root:

```text
python3 eval/corpus/validate.py
python3 -m unittest eval.corpus.test_contract
```

The second command needs `eval/corpus` on `PYTHONPATH`. Prefer:

```text
python3 eval/corpus/test_contract.py
```

## Projections

- **elicitation-input** — `project_description` and case identity.
  Golden Vision, Architecture, EARS requirements, and holdouts are
  absent.
- **job-site-input** — golden Sketch, Schematic, interface/runtime
  metadata, and worker-visible reference behavior checks. Holdout
  assets under `holdout/` are absent.
- **evaluator** — the full fixture, including holdout-asset
  references.

Workers never receive `holdout/` files. The validator checks that
boundary.

## Related specification

[Evaluation corpus schema and result contract](../../docs/architecture/evaluation-corpus.md)
