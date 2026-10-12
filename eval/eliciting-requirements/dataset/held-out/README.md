# Held-out corpus

This partition is the independently curated held-out regression corpus
for eliciting-requirements evaluation.

It is empty until a harness config executes this partition. Confirmed
failures from calibration route to `dataset/regression/` until a harness
runner executes this partition, ensuring newly confirmed failures are
run by existing regression workflows. Once a harness runner executes
`dataset/held-out/`, confirmed failures will be added here before a skill
change or a new baseline is promoted.

The v1 calibration sample itself is drawn from `dataset/cases/` and
`dataset/regression/`. See `calibration/v1/sample.yaml`.
