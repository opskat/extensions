// Where completion data comes from: the index list the page already loaded, and
// aliases and mappings fetched the first time a completion needs them. The page
// makes a new source whenever it (re)loads the cluster, so a refresh drops the
// cached aliases and mappings with the old index list.

import { mappingFields, type CompletionData } from "./completion";

export interface CompletionLoaders {
  indices: readonly string[];
  loadAliases(): Promise<string[]>;
  /** The `mapping` tool's result for a target (index, alias, pattern or list). */
  loadMapping(target: string): Promise<unknown>;
}

export interface CompletionSource {
  /** What is loaded now; never waits. */
  data: CompletionData;
  /** Loads what a completion needs, once per source. Resolves — never rejects — once it has settled. */
  ensure(needs: { aliases?: boolean; target?: string | null }): Promise<void>;
}

export function createCompletionSource({ indices, loadAliases, loadMapping }: CompletionLoaders): CompletionSource {
  let aliases: readonly string[] = [];
  let aliasesLoad: Promise<void> | null = null;
  const fields = new Map<string, readonly string[]>();
  const fieldLoads = new Map<string, Promise<void>>();

  // A load that fails leaves its part of the suggestions out: completion is a
  // convenience, and a user without the right to read aliases or a mapping
  // still types the request by hand. It is not retried until the next source.
  const settle = (p: Promise<void>) => p.catch(() => undefined);

  return {
    data: {
      indices,
      get aliases() {
        return aliases;
      },
      fields: (target) => fields.get(target) ?? [],
    },
    async ensure({ aliases: wantAliases, target }) {
      const pending: Promise<void>[] = [];
      if (wantAliases) {
        aliasesLoad ??= settle(
          loadAliases().then((list) => {
            aliases = list;
          })
        );
        pending.push(aliasesLoad);
      }
      if (target) {
        let load = fieldLoads.get(target);
        if (!load) {
          load = settle(
            loadMapping(target).then((m) => {
              fields.set(target, mappingFields(m));
            })
          );
          fieldLoads.set(target, load);
        }
        pending.push(load);
      }
      await Promise.all(pending);
    },
  };
}

/** Reads a `_cat/aliases?format=json&h=alias` body: each alias once. */
export function parseAliases(body: unknown): string[] {
  if (!Array.isArray(body)) throw new Error(`unexpected _cat/aliases response: ${JSON.stringify(body)}`);
  return [...new Set((body as { alias?: string }[]).flatMap((r) => (r.alias ? [r.alias] : [])))];
}
