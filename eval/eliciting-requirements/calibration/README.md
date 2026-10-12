# Human Calibration

Independent human calibration for the eliciting-requirements evaluation
baseline.

`v1/` records the sample and reviewer slots for `baselines/v1` _before_
scoring. Follow `v1/protocol.md`. Do not mark that baseline trusted until
two independent reviewers score the sample, agreement is computed, and
adjudication is complete.

Validate the recorded sample and the trusted-baseline gate with:

```text
python3 eval/eliciting-requirements/scripts/check_calibration.py
python3 eval/eliciting-requirements/scripts/test_check_calibration.py
```
