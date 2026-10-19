import tseslint from 'typescript-eslint';
export default [{ignores:['dist/**','node_modules/**']},{files:['**/*.ts','**/*.tsx'],languageOptions:{parser:tseslint.parser,parserOptions:{ecmaVersion:'latest',sourceType:'module',ecmaFeatures:{jsx:true}}},rules:{'no-debugger':'error','no-unreachable':'error','no-constant-condition':['error',{checkLoops:false}],'no-unsafe-finally':'error'}}];
