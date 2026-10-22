# Model card — `hivetrace/gliner-guard-omni` (gobellim guard L2)

This is the ML layer (L2) of gobellim's guard. It is **not** a safety guarantee: it is
the optional second layer on top of deterministic rules. Read it together with
[`docs/concepts/security-model.md`](../../docs/concepts/security-model.md).

## What it is

An ONNX artifact (`model.onnx`, fp32, opset 17) fed by an HF `tokenizer.json`,
consumed by `guard/gliner` under the `ort` build tag. The graph carries the encoder
plus a span head, a count head and a classifier; labels, thresholds, `prefix_ids`
and marker positions are read from `config.json`, not hardcoded, so a re-export
with a different pinned schema keeps working. Pipeline and graph contract:
[`SPEC.md`](./SPEC.md); export: [`export.py`](./export.py).

## Intended use

- Classify `safety` and the model's entity taxonomy, and find PII-like spans, on
  the `input`, `output` and `tool_result` rails.
- Sanity: it is one layer. The default build ships the deterministic L1 rules and
  the decision table, not the detector, decides the action (see `BR-26`).

Not intended for: structured ID validation (cards, INN, SNILS, IBAN — that is L1's
job), languages and scripts outside the training distribution, or as the sole
control on any path.

## Measured numbers

| Metric                              | Value                                                                              | Source                              |
| ----------------------------------- | ---------------------------------------------------------------------------------- | ----------------------------------- |
| Weights size                        | ≈1.2 GB (fp32; ≈0.4 GB int8)                                                       | `docs/reference/gobellim.md`    |
| Peak process RSS with the model     | ≈4.5 GB (darwin/arm64, fp32, 2 sessions, `intra_op_threads` = 4)                   | `docs/reference/gobellim.md`    |
| Latency at the token ceiling        | ≈0.37 s p50 per detection window                                                   | `docs/reference/gobellim.md` (D34) |
| Before the token ceiling existed    | ≈50–71 s and ≈7.3 GB on an 8000-rune window                                        | `docs/reference/gobellim.md` (D34) |
| Parity fixtures                     | 12/12 under `-tags ort`                                                            | `docs/reference/gobellim.md`            |
| Latency SLO (target, not a promise) | short input p50 ≤ 250 ms / p95 ≤ 400 ms; full tensor input p50 ≤ 800 ms per window | `docs/reference/gobellim.md` (A2/D53)   |
| Quality on public corpora (F1)      | **not published** — the L2 parity gate is not closed (weights are not in git)      | `docs/reference/gobellim.md` (DoD v1)   |

L1 numbers on a public corpus (with the caveat that they do not prove
generalisation) live in
[`docs/guides/guardrails.md`](../../docs/guides/guardrails.md).

## Known evasions and limitations

- **Adaptive attacks.** Rephrasing, other languages, encodings and long benign
  context beat detectors of this class. Assume it is evadable; that is why the
  deterministic controls carry the guarantee.
- **Token ceiling.** `Config.MaxTokens` (default 512, clamped to the model's
  `max_sequence_length`) caps the tensor input; the tail of a guard window is
  labelled by L1 only (`D34`). A long input is scanned in several windows, so the
  full cost grows with length (`D60`).
- **Natural text only.** Canonicalisation transliterates homoglyphs, so the model
  reads the original text (`UsesNaturalText()`, `D64`) — which means input it has
  never seen (mixed scripts, leetspeak) is where it degrades.
- **Thresholds are configuration.** A mismatch between the shipped artifact and
  the expected schema shows up as `ErrInvalidConfig`/`ErrGuardUnavailable`, not as
  a silent pass.
- **Resource cost.** Two sessions of this model dominate the process footprint;
  size your container from the measured RSS, not the file size
  (`docs/reference/gobellim.md`).
