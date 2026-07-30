import { reactive } from 'vue'
import { getState } from './api'
import type { Snapshot } from './types'

export const state = reactive<{snapshot:Snapshot|null; loading:boolean; error:string; socket?:WebSocket}>({
  snapshot:null, loading:false, error:''
})

export async function refresh() {
  state.loading=true
  try { state.snapshot=await getState(); state.error='' }
  catch(e:any) { state.error=e.code || e.message }
  finally { state.loading=false }
}

export function connectEvents() {
  state.socket?.close()
  const protocol=location.protocol==='https:'?'wss':'ws'
  const ws=new WebSocket(`${protocol}://${location.host}/api/v1/events`)
  state.socket=ws
  ws.onopen=()=>refresh()
  ws.onmessage=()=>refresh()
  ws.onclose=()=>setTimeout(connectEvents,2000)
}
