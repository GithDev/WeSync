import globals from 'globals';
import pluginJs from '@eslint/js';
import tseslint from 'typescript-eslint';
import pluginReact from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import prettier from 'eslint-config-prettier';

// ESLint owns *correctness* only. Formatting (quotes, semicolons, spacing,
// line length, trailing commas…) is delegated entirely to Prettier — see
// .prettierrc. `eslint-config-prettier` (last) switches off any stylistic
// rules from the shared configs so the two never fight.

/** @type {import('eslint').Linter.Config[]} */
export default [
  { ignores: ['dist', 'dev-dist'] },
  { files: ['**/*.{js,mjs,cjs,ts,jsx,tsx}'] },
  { languageOptions: { globals: { ...globals.browser, ...globals.node } } },
  pluginJs.configs.recommended,
  ...tseslint.configs.recommended,
  pluginReact.configs.flat.recommended,
  reactHooks.configs['recommended-latest'],
  {
    settings: {
      react: {
        version: 'detect',
      },
      'import/resolver': {
        alias: {
          map: [['@', './src']],
          extensions: ['.js', '.jsx', '.ts', '.tsx'],
        },
      },
    },
    rules: {
      '@typescript-eslint/no-unused-vars': 'warn',
      'react/react-in-jsx-scope': 'off',
      // Apostrophes in copy ("won't", "doesn't") are legitimate; escaping
      // them to &apos; only hurts source readability.
      'react/no-unescaped-entities': 'off',
    },
  },
  prettier,
];
