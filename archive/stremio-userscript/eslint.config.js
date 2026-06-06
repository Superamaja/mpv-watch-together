import js from "@eslint/js";
import globals from "globals";

export default [
  {
    ignores: ["archive/**", "dist/**"],
  },
  js.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        ...globals.browser,
        __DEFAULT_FIREBASE_CONFIG__: "readonly",
        __LUCIDE_ICONS__: "readonly",
      },
    },
    rules: {
      "no-redeclare": "off",
      "no-useless-escape": "off",
      "no-unused-vars": [
        "warn",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
        },
      ],
    },
  },
];
