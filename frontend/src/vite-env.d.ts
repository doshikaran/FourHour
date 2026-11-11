/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Backend base URL including /api (CONTRACTS.md section 2). */
  readonly VITE_API_BASE_URL?: string;
  /** "true" serves every endpoint and the replay stream from src/mock. Frontend-only build switch. */
  readonly VITE_USE_MOCK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
