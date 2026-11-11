/**
 * K3 placeholder: shows that a page reaches its endpoint through the API client, with loading,
 * error and loaded states. F1 replaces this with the real shared state components.
 */
import type { ReactNode } from 'react';
import { ApiError } from '../api/client';
import { useAsync } from '../lib/useAsync';

export function Probe<T>(props: { what: string; load: () => Promise<T>; loadKey: string; summary: (data: T) => ReactNode }) {
  const s = useAsync(props.load, props.loadKey);
  if (s.status === 'loading') {
    return (
      <p className="probe" role="status">
        Loading {props.what}…
      </p>
    );
  }
  if (s.status === 'error') {
    const e = s.error;
    return (
      <div className="probe probe-error" role="alert">
        <p>
          <strong>Could not load {props.what}.</strong> {e.message}
          {e instanceof ApiError ? ` (${e.endpoint}, code ${e.code}${e.paypalDebugId ? `, PayPal debug ID ${e.paypalDebugId}` : ''})` : ''}
        </p>
        <button type="button" onClick={s.reload}>
          Try again
        </button>
      </div>
    );
  }
  return (
    <div className="probe probe-ok" data-testid="probe-ok">
      Loaded {props.what}: {props.summary(s.data)}
    </div>
  );
}
