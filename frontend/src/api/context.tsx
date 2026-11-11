import { createContext, useContext, type ReactNode } from 'react';
import { API_BASE_URL, USE_MOCK } from '../config';
import { createMockApi } from '../mock/mockApi';
import { createHttpApi, type FourHourApi } from './client';

/** Chooses the implementation from the one switch in src/config.ts. */
export function createDefaultApi(): FourHourApi {
  return USE_MOCK ? createMockApi() : createHttpApi({ baseUrl: API_BASE_URL });
}

const ApiContext = createContext<FourHourApi | null>(null);

export function ApiProvider({ api, children }: { api: FourHourApi; children: ReactNode }) {
  return <ApiContext.Provider value={api}>{children}</ApiContext.Provider>;
}

export function useApi(): FourHourApi {
  const api = useContext(ApiContext);
  if (!api) throw new Error('useApi must be used inside <ApiProvider>.');
  return api;
}
