<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Phone, PhoneOff, Grid3X3, Radio, Pause, Play, Voicemail, ExternalLink, AudioLines } from '@lucide/vue'
import type { Call } from '../types'
import { api, post } from '../api'
import { connectCallMedia, type CallMediaSession, type CallMediaStatus } from '../media'
import { playDTMFKey } from '../callAudio'
const call=ref<Call|null>(null),error=ref(''),keypad=ref(false),busy=ref(false),mediaSession=ref<CallMediaSession>(),mediaConnecting=ref(false),pollTimer=ref<number>()
const voicemailEnabled=ref(false)
const media=ref<CallMediaStatus>({phase:'negotiating',micLevel:0,packetsSent:0,bytesSent:0})
const dtmfHistory=ref('')
const telegramBrowser=/Telegram/i.test(navigator.userAgent)
const ended=computed(()=>call.value?.state==='ended'||call.value?.state==='failed')
const mediaLabel=computed(()=>{
  if(media.value.phase==='bridged')return '通话语音已连接'
  if(media.value.phase==='waiting_for_permission')return '等待麦克风权限'
  if(media.value.phase==='recovering')return '正在恢复通话语音…'
  if(media.value.phase==='failed'||media.value.phase==='timeout')return '通话语音连接失败'
  return '正在连接通话语音…'
})
const keys=['1','2','3','4','5','6','7','8','9','*','0','#']
async function refresh(){
  try{
    const current=await api<Call>('/api/v1/temp/call')
    call.value=current
    if(current.state==='active'&&!current.held&&current.mediaOwner!=='voicemail')void startMedia(current.id)
  }catch{if(!call.value)error.value='来电链接无效或已经结束'}
}
onMounted(async()=>{
  await refresh()
  if(call.value)try{voicemailEnabled.value=(await api<{voicemailEnabled:boolean}>(`/api/v1/temp/call/${call.value.id}/capabilities`)).voicemailEnabled}catch{}
  pollTimer.value=window.setInterval(refresh,1000)
})
async function startMedia(id:string){
  if(mediaSession.value||mediaConnecting.value||call.value?.held||call.value?.mediaOwner==='voicemail')return
  mediaConnecting.value=true
  try{mediaSession.value=await connectCallMedia(id,true,value=>media.value=value)}
  catch(e:any){error.value=e.code||e.message}
  finally{mediaConnecting.value=false}
}
async function act(name:string,body:any={}){
  const id=call.value?.id
  if(!id)return
  busy.value=true
  try{
    call.value=await post(`/api/v1/temp/call/${id}/${name}`,body) as Call
    if(name==='answer'||name==='resume'){
      for(let i=0;i<20&&call.value?.state!=='active';i++){
        await new Promise(r=>setTimeout(r,250))
        call.value=await api('/api/v1/temp/call')
      }
      if(call.value?.state==='active'&&!call.value.held)await startMedia(id)
    }
  }catch(e:any){error.value=e.code||e.message}
  finally{busy.value=false}
}
async function sendDTMF(key:string){playDTMFKey(key,150);dtmfHistory.value=(dtmfHistory.value+key).slice(-48);await act('dtmf',{key})}
function openExternal(){window.open(location.href,'_blank','noopener')}
onBeforeUnmount(()=>{mediaSession.value?.disconnect();if(pollTimer.value)clearInterval(pollTimer.value)})
</script>
<template><main class="temp-page"><div class="temp-brand"><Radio/>onSIM 临时通话</div>
<button v-if="telegramBrowser" class="telegram-browser-note" @click="openExternal"><ExternalLink/>Telegram 内置浏览器可能无法持续使用麦克风，点击在外部浏览器打开</button>
<section v-if="call" class="temp-call" :class="{held:call.held}"><div class="ring"><Pause v-if="call.held"/><Phone v-else/></div><p class="eyebrow">{{ended?'通话已结束':call.held?'通话已挂起':call.state==='incoming'?'来电':'通话中'}}</p><h1>{{call.displayName||call.number}}</h1><p>{{call.held?'对方正在等待':call.filter.label||'电话来电'}}</p>
<div v-if="call.state==='active'&&!call.held&&call.mediaOwner!=='voicemail'" class="temp-media" :class="media.phase"><AudioLines/><span>{{mediaLabel}}</span></div>
<div v-if="keypad&&!call.held&&!ended"><div class="dtmf-history"><small>按键记录</small><b>{{dtmfHistory||'尚未输入'}}</b></div><div class="keypad temp-keys"><button v-for="k in keys" :key="k" @click="sendDTMF(k)"><b>{{k}}</b></button></div></div>
<div v-if="!ended" class="temp-actions-grid">
  <button v-if="call.state==='incoming'" class="temp-action answer" :disabled="busy" @click="act('answer')"><Phone/><span>接听</span></button>
  <button v-if="call.state==='incoming'||!call.held" class="temp-action hold" :disabled="busy" @click="act('hold')"><Pause/><span>挂起</span></button>
  <button v-if="call.held" class="temp-action answer" :disabled="busy" @click="act('resume')"><Play/><span>恢复</span></button>
  <button v-if="voicemailEnabled&&(call.state==='incoming'||call.held)" class="temp-action voicemail" :disabled="busy" @click="act('voicemail')"><Voicemail/><span>转留言</span></button>
  <button v-if="call.state==='active'&&!call.held&&call.mediaOwner!=='voicemail'" class="temp-action" @click="keypad=!keypad"><Grid3X3/><span>键盘</span></button>
  <button class="temp-action danger" :disabled="busy" @click="act('hangup')"><PhoneOff/><span>{{call.state==='incoming'?'拒接':'挂断'}}</span></button>
</div><p v-if="error" class="media-error">{{error}}</p><small>此页面只能控制当前通话，通话结束后权限失效。</small></section>
<section v-else class="temp-call error-state"><PhoneOff/><h1>无法接听</h1><p>{{error||'正在连接设备…'}}</p></section></main></template>
