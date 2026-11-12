# Public acceptance record: UI07

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI07 | 2026-11-03 | Shared modern native choices and themed native picker with fallback; Your garden routes to collection; Today round-robin uploaded covers per visit/refresh, actual plant headline, stable during in-page actions. Native-picker Escape closes picker before parent | `.artifacts/agents/UI07/` (SUMMARY.md, full-unit.log, build.log, browser-results.json, live-check.json, integrated-hashes.json, popup screenshots) | 350 frontend units in39 files passed; production build; phone390/desktop1440 × both themes: six native choices44px/16px matching text inputs, themed popup, native selection/dirty dismissal, forced-colors/disabled, modal and filter popup Escape, garden route, consecutive visits + hard refresh rotate; axe0/no browser errors. Exact tested bundle on4300/4311; API ready200. No backend/contracts/dependencies or real-owner account mutations. |

# UI07

Owner reports inconsistent old dropdowns on Add Plant, Your garden wrongly opens settings, Today always shows Lime. Implement shared modern native select presentation (respect prior native-choice preference), garden collection route, round-robin per-visit hero among existing cover-photo plants. No automatic slideshow or changes during mutations. Runtime via sequential local Strata in isolated UI07 source copy; root tests/review/format/integration. Scope as packet; no commits/credentials/dependencies/backend.

Verified native base-select popup Escape bubbles to parent with defaultPrevented=false and select.matches(":open")=true. Add shared guarded Escape utility to UiDialog and collection filter drawer so first Escape closes native picker, next closes parent. Include styles corrections for :open arrow selector and disabled opacity.
