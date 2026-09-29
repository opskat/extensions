// Monaco glue for the console and the documents tab's DSL box: the page's own
// languages (highlighting) and completion, on the host's Monaco instance. The
// instance is shared with the whole app, so the languages have ids of their own,
// and everything registered here is disposed when the last editor using it goes.
// What to highlight and suggest is decided in es/console.ts and es/completion.ts.

import type { IDisposable, Monaco, MonacoEditor } from "@opskat/host-ui";
import type { Suggestion, SuggestionKind } from "../../es/completion";

export const CONSOLE_LANGUAGE = "opskat-ext-es-console";
export const DSL_LANGUAGE = "opskat-ext-es-dsl";

// Token names the host's themes (built on vs / vs-dark) already colour.
const JSON_RULES = [
  [/\s+/, "white"],
  [/"(?:[^"\\]|\\.)*"(?=\s*:)/, "string.key.json"],
  [/"(?:[^"\\]|\\.)*"?/, "string.value.json"],
  [/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/, "number"],
  [/\b(?:true|false|null)\b/, "keyword.json"],
  [/[{}[\]]/, "delimiter.bracket"],
  [/[,:]/, "delimiter"],
];

const METHOD = "GET|POST|PUT|DELETE|HEAD";

const CONSOLE_TOKENS = {
  ignoreCase: true,
  tokenizer: {
    root: [
      [/^\s*#.*$/, "comment"],
      [new RegExp(`^(\\s*)(${METHOD})(\\s*)$`), ["white", "keyword", "white"]],
      [new RegExp(`^(\\s*)(${METHOD})(\\s+)(\\S+)(.*)$`), ["white", "keyword", "white", "type", "white"]],
      ...JSON_RULES,
    ],
  },
};

const DSL_TOKENS = { tokenizer: { root: JSON_RULES } };

const JSON_CONFIGURATION = {
  brackets: [
    ["{", "}"],
    ["[", "]"],
  ],
  autoClosingPairs: [
    { open: "{", close: "}" },
    { open: "[", close: "]" },
    { open: '"', close: '"', notIn: ["string"] },
  ],
  surroundingPairs: [
    { open: "{", close: "}" },
    { open: "[", close: "]" },
    { open: '"', close: '"' },
  ],
};

let users = 0;
let registered: IDisposable[] = [];

/** Registers the page's languages for one more editor; the returned release undoes it for the last one. */
function acquireLanguages(monaco: Monaco): () => void {
  if (users++ === 0) {
    const known = new Set(monaco.languages.getLanguages().map((l) => l.id));
    for (const id of [CONSOLE_LANGUAGE, DSL_LANGUAGE]) if (!known.has(id)) monaco.languages.register({ id });
    registered = [
      monaco.languages.setMonarchTokensProvider(CONSOLE_LANGUAGE, CONSOLE_TOKENS),
      monaco.languages.setMonarchTokensProvider(DSL_LANGUAGE, DSL_TOKENS),
      monaco.languages.setLanguageConfiguration(CONSOLE_LANGUAGE, {
        ...JSON_CONFIGURATION,
        comments: { lineComment: "#" },
      }),
      monaco.languages.setLanguageConfiguration(DSL_LANGUAGE, JSON_CONFIGURATION),
    ];
  }
  return () => {
    if (--users === 0) {
      for (const d of registered) d.dispose();
      registered = [];
    }
  };
}

/** A completion: replace from offset `from` up to the cursor with one of `items`. */
export interface EditorCompletion {
  from: number;
  items: Suggestion[];
}

/**
 * Editor options for the page's languages. JSON keys are string tokens, and the
 * host's editor does not pop suggestions up inside strings by default.
 */
export const ES_EDITOR_OPTIONS = { quickSuggestions: { other: true, comments: false, strings: true } };

/**
 * Puts an editor on one of the page's languages and gives it completion.
 * `complete` gets the editor's text and the cursor's offset; it may wait for
 * data, and null offers nothing. Undone when the editor is disposed.
 */
export function setupEsEditor(
  editor: MonacoEditor,
  monaco: Monaco,
  language: typeof CONSOLE_LANGUAGE | typeof DSL_LANGUAGE,
  complete: (text: string, offset: number) => Promise<EditorCompletion | null>
): void {
  const model = editor.getModel();
  if (!model) return;
  const release = acquireLanguages(monaco);
  monaco.editor.setModelLanguage(model, language);

  const kinds: Record<SuggestionKind, number> = {
    method: monaco.languages.CompletionItemKind.Method,
    api: monaco.languages.CompletionItemKind.Function,
    index: monaco.languages.CompletionItemKind.Module,
    alias: monaco.languages.CompletionItemKind.Reference,
    key: monaco.languages.CompletionItemKind.Property,
    field: monaco.languages.CompletionItemKind.Field,
  };
  // Every editor on the language registers its own provider; each answers for its own model only.
  const provider = monaco.languages.registerCompletionItemProvider(language, {
    triggerCharacters: ['"', "/", "."],
    async provideCompletionItems(asked, position) {
      if (asked !== model) return { suggestions: [] };
      const result = await complete(model.getValue(), model.getOffsetAt(position));
      if (!result) return { suggestions: [] };
      const start = model.getPositionAt(result.from);
      const range = {
        startLineNumber: start.lineNumber,
        startColumn: start.column,
        endLineNumber: position.lineNumber,
        endColumn: position.column,
      };
      return {
        suggestions: result.items.map((s, i) => ({
          label: s.label,
          kind: kinds[s.kind],
          insertText: s.insertText,
          range,
          // Keep the engine's order: methods, APIs, then the cluster's names; DSL keys, then fields.
          sortText: String(i).padStart(4, "0"),
          // A method put in with its space is followed by its path: offer that straight away.
          ...(s.kind === "method" &&
            s.insertText.endsWith(" ") && { command: { id: "editor.action.triggerSuggest", title: "" } }),
        })),
      };
    },
  });

  const disposed = editor.onDidDispose(() => {
    provider.dispose();
    release();
    disposed.dispose();
  });
}

/** Waits for `p` at most `ms`: completion shows what it has rather than hang on a slow cluster. */
export function atMost(p: Promise<unknown>, ms: number): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    p.then(() => {
      clearTimeout(timer);
      resolve();
    });
  });
}
