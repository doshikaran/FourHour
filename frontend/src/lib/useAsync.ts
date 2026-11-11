import { useCallback, useEffect, useState } from 'react';

export type AsyncState<T> =
  | { status: 'loading' }
  | { status: 'error'; error: Error }
  | { status: 'ok'; data: T };

/** Runs `fn` on mount and whenever `key` changes. Ignores results that arrive after unmount or a newer run. */
export function useAsync<T>(fn: () => Promise<T>, key: string): AsyncState<T> & { reload: () => void } {
  const [state, setState] = useState<AsyncState<T>>({ status: 'loading' });
  const [run, setRun] = useState(0);
  const reload = useCallback(() => setRun((n) => n + 1), []);

  useEffect(() => {
    let live = true;
    setState({ status: 'loading' });
    fn().then(
      (data) => live && setState({ status: 'ok', data }),
      (e: unknown) => live && setState({ status: 'error', error: e instanceof Error ? e : new Error(String(e)) }),
    );
    return () => {
      live = false;
    };
    // `fn` is recreated every render on purpose; `key` and `run` say when to fetch again.
  }, [key, run]);

  return { ...state, reload };
}
