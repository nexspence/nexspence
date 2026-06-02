import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.recommended,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      // Full react-hooks v7 ruleset (rules-of-hooks, exhaustive-deps, and the
      // React Compiler lints: purity, immutability, set-state-in-effect, etc.).
      // Pulled from the plugin's recommended-latest config — referencing only its
      // `rules` because that config's `plugins` key is in legacy (eslintrc) array
      // form, which the flat-config loader rejects; the plugin itself is already
      // registered in `plugins` above.
      ...reactHooks.configs['recommended-latest'].rules,
      // The two React-Compiler dataflow lints below misfire on idiomatic
      // hand-written React (this codebase does not run the React Compiler):
      //  - set-state-in-effect flags every legit on-mount fetch, prop->state
      //    sync on modal open, conditional reset, and URL-driven effect.
      //  - immutability flagged a plain XMLHttpRequest (xhr.setRequestHeader) as
      //    if it were React state.
      // All other strict rules (rules-of-hooks, set-state-in-render, purity,
      // static-components, refs, globals, ...) stay enabled.
      'react-hooks/set-state-in-effect': 'off',
      'react-hooks/immutability': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
    },
  },
)
