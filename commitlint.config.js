// Conventional Commits rules for omnivya-apm-app. This underpins release-please's
// version inference and CHANGELOG generation. package.json has no "type": "module",
// so this file is CommonJS (module.exports) — unlike the browser SDK SDK's ESM variant.
module.exports = {
  extends: ['@commitlint/config-conventional'],
  rules: {
    // Keep as warn: several PR commits already have single-paragraph bodies >100 chars,
    // and rewriting pushed history requires force-push (disallowed).
    'body-max-line-length': [1, 'always', 100],
    // Acronyms in subjects (VPA, YAML) trip sentence-case; rewriting pushed commits
    // would need force-push (disallowed).
    'subject-case': [1, 'never', ['sentence-case', 'start-case', 'pascal-case', 'upper-case']],
  },
};
