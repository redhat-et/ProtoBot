# Held-out corpus

This partition is the independently curated held-out regression corpus
for eliciting-requirements evaluation.

It is empty until calibration confirms a failure. Confirmed failures
are added here before the skill changes or a new baseline is promoted.
Do not use these cases to tune the visible development corpus or the
known regression set in `dataset/regression/`.

The v1 calibration sample itself is drawn from `dataset/cases/` and
`dataset/regression/`. See `calibration/v1/sample.yaml`.
