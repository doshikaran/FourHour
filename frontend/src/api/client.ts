/**
 * The one module through which the frontend talks to the backend (CONTRACTS.md section 4 and 5).
 * `FourHourApi` is implemented twice: `createHttpApi` (the real backend) and `createMockApi`
 * (src/mock). Pages get the instance from `useApi()` (src/api/context.tsx); nothing else calls fetch.
 *
 * Not here on purpose: GET /api/paypal/vault/return and /vault/cancel are browser redirects that
 * PayPal drives, and POST /api/paypal/webhook is called by PayPal, never by the frontend.
 */
import type {
  AnalyticsArtifacts,
  AnalyticsName,
  ApiErrorBody,
  CancelPolicyResponse,
  ChargePremiumRequest,
  ChargePremiumResponse,
  ClaimId,
  ClaimsResponse,
  CountiesResponse,
  DemoResponse,
  CreateMemberRequest,
  CreateMemberResponse,
  CreatePolicyRequest,
  CreatePolicyResponse,
  DeviceEntriesResponse,
  DeviceId,
  HealthResponse,
  LedgerResponse,
  PoliciesResponse,
  PolicyDetailResponse,
  PolicyId,
  PostEntriesRequest,
  PostEntriesResponse,
  QuoteRequest,
  QuoteResponse,
  RegisterDeviceRequest,
  RegisterDeviceResponse,
  ResetReplayResponse,
  ScenariosResponse,
  StartReplayRequest,
  StartReplayResponse,
  StopReplayResponse,
  ClaimDetailResponse,
} from './types';
import { openEventSourceStream, type ReplayStreamHandlers, type ReplayStreamHandle } from './stream';

export type { ReplayStreamHandlers, ReplayStreamHandle, StreamStatus } from './stream';

export interface FourHourApi {
  /** "mock" when serving fixtures from src/mock. The UI shows a banner in that case. */
  readonly mode: 'mock' | 'http';

  health(): Promise<HealthResponse>;
  counties(): Promise<CountiesResponse>;
  quote(req: QuoteRequest): Promise<QuoteResponse>;
  /** GET /api/demo (1.8): the demo policy created at startup, or policy_id null. */
  demo(): Promise<DemoResponse>;

  createMember(req: CreateMemberRequest): Promise<CreateMemberResponse>;
  createPolicy(req: CreatePolicyRequest): Promise<CreatePolicyResponse>;
  policy(id: PolicyId): Promise<PolicyDetailResponse>;
  policies(): Promise<PoliciesResponse>;
  chargePremium(id: PolicyId, req: ChargePremiumRequest): Promise<ChargePremiumResponse>;
  cancelPolicy(id: PolicyId): Promise<CancelPolicyResponse>;

  registerDevice(req: RegisterDeviceRequest): Promise<RegisterDeviceResponse>;
  postEntries(req: PostEntriesRequest): Promise<PostEntriesResponse>;
  deviceEntries(id: DeviceId, limit?: number): Promise<DeviceEntriesResponse>;

  scenarios(): Promise<ScenariosResponse>;
  startReplay(req: StartReplayRequest): Promise<StartReplayResponse>;
  stopReplay(): Promise<StopReplayResponse>;
  resetReplay(): Promise<ResetReplayResponse>;
  openReplayStream(handlers: ReplayStreamHandlers): ReplayStreamHandle;

  claims(): Promise<ClaimsResponse>;
  claim(id: ClaimId): Promise<ClaimDetailResponse>;
  ledger(): Promise<LedgerResponse>;
  analytics<K extends AnalyticsName>(name: K): Promise<AnalyticsArtifacts[K]>;
}

/** Every failure the client reports. `status` is 0 when no HTTP response arrived. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly paypalDebugId: string | null;
  readonly endpoint: string;

  constructor(opts: { status: number; code: string; message: string; paypalDebugId: string | null; endpoint: string }) {
    super(opts.message);
    this.name = 'ApiError';
    this.status = opts.status;
    this.code = opts.code;
    this.paypalDebugId = opts.paypalDebugId;
    this.endpoint = opts.endpoint;
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function isApiErrorBody(v: unknown): v is ApiErrorBody {
  if (!isRecord(v) || !isRecord(v.error)) return false;
  const e = v.error;
  return (
    typeof e.code === 'string' &&
    typeof e.message === 'string' &&
    (e.paypal_debug_id === null || typeof e.paypal_debug_id === 'string' || e.paypal_debug_id === undefined)
  );
}

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface HttpApiOptions {
  /** Backend base URL including /api, e.g. "http://localhost:8080/api". */
  baseUrl: string;
  fetchImpl?: FetchLike;
  eventSourceImpl?: typeof EventSource;
}

export function createHttpApi(opts: HttpApiOptions): FourHourApi {
  const base = opts.baseUrl.replace(/\/+$/, '');
  const doFetch: FetchLike = opts.fetchImpl ?? ((input, init) => fetch(input, init));

  async function request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
    const endpoint = `${method} ${path}`;
    if (!base) {
      throw new ApiError({
        status: 0,
        code: 'not_configured',
        message: 'VITE_API_BASE_URL is not set, so the app cannot reach the backend. Set it in .env or run with the mock.',
        paypalDebugId: null,
        endpoint,
      });
    }
    let res: Response;
    try {
      const init: RequestInit = { method, headers: { Accept: 'application/json' } };
      if (body !== undefined) {
        init.headers = { Accept: 'application/json', 'Content-Type': 'application/json' };
        init.body = JSON.stringify(body);
      }
      res = await doFetch(`${base}${path}`, init);
    } catch (e: unknown) {
      throw new ApiError({
        status: 0,
        code: 'network_error',
        message: `Could not reach the backend at ${base} (${e instanceof Error ? e.message : String(e)}). Check that it is running and try again.`,
        paypalDebugId: null,
        endpoint,
      });
    }
    const text = await res.text();
    let parsed: unknown = undefined;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = undefined;
      }
    }
    if (!res.ok) {
      if (isApiErrorBody(parsed)) {
        throw new ApiError({
          status: res.status,
          code: parsed.error.code,
          message: parsed.error.message,
          paypalDebugId: parsed.error.paypal_debug_id ?? null,
          endpoint,
        });
      }
      throw new ApiError({
        status: res.status,
        code: 'http_error',
        message: `The backend answered ${res.status} ${res.statusText} without an error body.`,
        paypalDebugId: null,
        endpoint,
      });
    }
    if (parsed === undefined) {
      throw new ApiError({
        status: res.status,
        code: 'bad_response',
        message: 'The backend answered without a JSON body.',
        paypalDebugId: null,
        endpoint,
      });
    }
    // The backend is trusted to follow CONTRACTS.md; shapes are not re-validated here.
    return parsed as T;
  }

  const get = <T>(path: string) => request<T>('GET', path);
  const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {});
  const enc = encodeURIComponent;

  return {
    mode: 'http',
    health: () => get('/health'),
    counties: () => get('/counties'),
    quote: (req) => post('/quotes', req),
    demo: () => get('/demo'),
    createMember: (req) => post('/members', req),
    createPolicy: (req) => post('/policies', req),
    policy: (id) => get(`/policies/${enc(id)}`),
    policies: () => get('/policies'),
    chargePremium: (id, req) => post(`/policies/${enc(id)}/charge-premium`, req),
    cancelPolicy: (id) => post(`/policies/${enc(id)}/cancel`),
    registerDevice: (req) => post('/witness/devices', req),
    postEntries: (req) => post('/witness/entries', req),
    deviceEntries: (id, limit = 200) => get(`/witness/devices/${enc(id)}/entries?limit=${limit}`),
    scenarios: () => get('/replay/scenarios'),
    startReplay: (req) => post('/replay/start', req),
    stopReplay: () => post('/replay/stop'),
    resetReplay: () => post('/replay/reset'),
    openReplayStream: (handlers) =>
      openEventSourceStream(`${base}/replay/stream`, handlers, opts.eventSourceImpl),
    claims: () => get('/claims'),
    claim: (id) => get(`/claims/${enc(id)}`),
    ledger: () => get('/ledger'),
    analytics: (name) => get(`/analytics/${name}`),
  };
}
