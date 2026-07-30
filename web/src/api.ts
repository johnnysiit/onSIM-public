import type { Snapshot } from './types'

export class ApiError extends Error {
  constructor(public code: string, public status: number) { super(code) }
}

function idempotencyKey(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  return [...bytes].map((byte, index) =>
    ([4, 6, 8, 10].includes(index) ? '-' : '') + byte.toString(16).padStart(2, '0')
  ).join('')
}

export async function api<T>(url:string, options:RequestInit={}):Promise<T> {
  const res = await fetch(url, {
    ...options,
    headers: { 'Content-Type':'application/json', ...(options.headers || {}) },
    credentials:'same-origin'
  })
  const body = await res.json().catch(()=>({}))
  if (!res.ok) throw new ApiError(body?.error?.code || `HTTP_${res.status}`, res.status)
  return body as T
}

export const post = <T>(url:string, body:unknown={}) => api<T>(url,{method:'POST',body:JSON.stringify(body),headers:{'Idempotency-Key':idempotencyKey()}})
export const put = <T>(url:string, body:unknown) => api<T>(url,{method:'PUT',body:JSON.stringify(body),headers:{'Idempotency-Key':idempotencyKey()}})
export const del = <T>(url:string) => api<T>(url,{method:'DELETE',headers:{'Idempotency-Key':idempotencyKey()}})
export async function uploadWav<T>(url:string, wav:Blob):Promise<T>{
  const res=await fetch(url,{method:'POST',body:wav,credentials:'same-origin',headers:{'Content-Type':'audio/wav','Idempotency-Key':idempotencyKey()}})
  const body=await res.json().catch(()=>({}))
  if(!res.ok)throw new ApiError(body?.error?.code||`HTTP_${res.status}`,res.status)
  return body as T
}
const arrayOrEmpty = <T>(value:T[]|null|undefined):T[] => Array.isArray(value) ? value : []

export async function getState():Promise<Snapshot> {
  const snapshot=await api<Snapshot>('/api/v1/state')
  return {
    ...snapshot,
    calls:arrayOrEmpty(snapshot.calls),
    messages:arrayOrEmpty(snapshot.messages),
    conversations:arrayOrEmpty(snapshot.conversations),
    rules:arrayOrEmpty(snapshot.rules),
    recordings:arrayOrEmpty(snapshot.recordings),
    device:{...snapshot.device,degraded:arrayOrEmpty(snapshot.device?.degraded)},
    settings:{...snapshot.settings,autoBlockCategories:arrayOrEmpty(snapshot.settings?.autoBlockCategories)}
  }
}
