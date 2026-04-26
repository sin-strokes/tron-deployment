## Summary

<!-- What does this change, in one sentence? -->

## Why

<!-- The motivation. Link to the issue if there is one. -->
<!-- Closes #... -->

## Implementation notes

<!-- Anything non-obvious about the approach. Tradeoffs you considered. -->

## Verification

<!-- How a reviewer can convince themselves it works. Pick the boxes that apply. -->

- [ ] `make build && make test` passes
- [ ] `make lint` passes (or new findings are justified)
- [ ] `trond config validate examples/*.yaml` still passes
- [ ] Manually verified end-to-end (paste the relevant command + output below)

```
(paste verification command output here)
```

## Compatibility

- [ ] No breaking change to intent schema
- [ ] No breaking change to state.json format
- [ ] CLI flags / output shape unchanged for downstream consumers

If you ticked any "breaking" box, add a `BREAKING CHANGE:` line to the
commit message body and update `CHANGELOG.md`.
