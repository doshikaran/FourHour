/**
 * The only place the frontend reads build-time environment variables.
 *
 * The mock switch: VITE_USE_MOCK=true serves every endpoint and the replay stream from src/mock.
 * Anything else uses the real backend at VITE_API_BASE_URL (CONTRACTS.md section 2).
 */
export const USE_MOCK: boolean = import.meta.env.VITE_USE_MOCK === 'true';
export const API_BASE_URL: string = import.meta.env.VITE_API_BASE_URL ?? '';
