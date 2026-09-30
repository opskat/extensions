import { afterEach, describe, expect, it, vi } from "vitest";
import { loadConsoles, saveConsoles } from "./host";
import type { SavedConsole } from "./es/tabs";

/**
 * A host whose actions run concurrently, as the real one does (each IPC call on
 * its own goroutine, several WASM instances): console.save stores its consoles
 * only when the test lets it finish, console.load reads whatever is stored then.
 */
function concurrentHost() {
  const stored = new Map<number, SavedConsole[]>();
  const pendingSaves: (() => void)[] = [];
  const executeAction = vi.fn(
    (_ext: string, action: string, args: { consoles?: SavedConsole[] }, _onEvent: unknown, assetId: number) => {
      if (action === "console.load") return Promise.resolve({ consoles: stored.get(assetId) ?? [] });
      return new Promise<unknown>((resolve) => {
        pendingSaves.push(() => {
          stored.set(assetId, args.consoles!);
          resolve({});
        });
      });
    }
  );
  vi.stubGlobal("window", { __OPSKAT_EXT__: { api: { executeAction } } });
  // Newest first: a host running calls concurrently may finish them in any order.
  const finishSaves = () => pendingSaves.splice(0).reverse().forEach((finish) => finish());
  return { stored, finishSaves };
}

// The call order is kept per asset for the whole page session (module state), so
// each test uses assets of its own.
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("console persistence calls", () => {
  it("loads an asset's consoles only after its earlier save has been stored", async () => {
    // The page closing saves its last edits; opening the same asset again right away
    // loads them — the load must not read the store before that save lands.
    const host = concurrentHost();
    const saved = saveConsoles(1, [{ number: 1, text: "GET /_cat/indices" }]);
    const loaded = loadConsoles(1);
    await Promise.resolve();
    host.finishSaves();
    await saved;
    expect(await loaded).toEqual([{ number: 1, text: "GET /_cat/indices" }]);
  });

  it("stores an asset's saves in the order they were made", async () => {
    const host = concurrentHost();
    const first = saveConsoles(1, [{ number: 1, text: "old" }]);
    const second = saveConsoles(1, [{ number: 1, text: "new" }]);
    // Finish whatever has reached the host until both are done.
    for (let i = 0; i < 5; i++) {
      await Promise.resolve();
      host.finishSaves();
    }
    await Promise.all([first, second]);
    expect(host.stored.get(1)).toEqual([{ number: 1, text: "new" }]);
  });

  it("does not hold one asset's load behind another asset's save", async () => {
    concurrentHost();
    void saveConsoles(3, [{ number: 1, text: "a" }]); // never finishes
    await expect(loadConsoles(4)).resolves.toEqual([]);
  });

  it("still loads after an earlier save failed", async () => {
    const executeAction = vi.fn((_ext: string, action: string) =>
      action === "console.save" ? Promise.reject(new Error("kv full")) : Promise.resolve({ consoles: [] })
    );
    vi.stubGlobal("window", { __OPSKAT_EXT__: { api: { executeAction } } });
    await expect(saveConsoles(5, [])).rejects.toThrow("kv full");
    await expect(loadConsoles(5)).resolves.toEqual([]);
  });
});
