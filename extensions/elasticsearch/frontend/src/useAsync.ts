import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "./es/errors";

export type AsyncState<T> =
  | { status: "loading" }
  | { status: "error"; error: string }
  | { status: "ready"; data: T };

/**
 * Runs `load` when `deps` change (and `enabled` is true) and on reload(). The
 * previous run is aborted, and with it the tool call it is waiting on.
 */
export function useAsync<T>(
  load: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
  enabled = true
): { state: AsyncState<T>; reload: () => void } {
  const [state, setState] = useState<AsyncState<T>>({ status: "loading" });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    const ctrl = new AbortController();
    setState({ status: "loading" });
    load(ctrl.signal).then(
      (data) => {
        if (!ctrl.signal.aborted) setState({ status: "ready", data });
      },
      (err) => {
        if (!ctrl.signal.aborted) setState({ status: "error", error: errorMessage(err) });
      }
    );
    return () => ctrl.abort();
    // load is re-created every render; deps is what decides a reload.
  }, [...deps, enabled, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { state, reload };
}
