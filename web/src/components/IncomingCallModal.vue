<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { PhoneCall, PhoneOff, Smartphone, Pause, Voicemail } from '@lucide/vue'
import type { Call } from '../types'
import { post } from '../api'
import { callAudioState, enableCallAudio, startIncomingRingtone } from '../callAudio'
import { state } from '../state'

const props=defineProps<{call:Call;operator?:string}>()
const busy=ref<'answer'|'reject'|'hold'|'voicemail'|''>(''),error=ref('')
const now=ref(Date.now())
let titleTimer:number|undefined,countdownTimer:number|undefined,stopRingtone:(()=>void)|undefined,notification:Notification|undefined
const originalTitle=document.title
const voicemailRemaining=computed(()=>{
  const settings=state.snapshot?.settings
  if(!settings?.voicemailEnabled)return 0
  const elapsed=Math.floor((now.value-new Date(props.call.startedAt).getTime())/1000)
  return Math.max(0,(settings.voicemailTimeoutSeconds||30)-elapsed)
})

async function action(name:'answer'|'hangup'|'hold'|'voicemail'){
  if(busy.value)return
  busy.value=name==='hangup'?'reject':name
  error.value=''
  stopReminders()
  try{await post(`/api/v1/calls/${props.call.id}/${name}`)}
  catch(e:any){error.value=e.code||e.message;busy.value='';startReminders()}
}

function startReminders(){
  if(titleTimer)return
  let alternate=false
  titleTimer=window.setInterval(()=>{alternate=!alternate;document.title=alternate?'📞 新来电':originalTitle},700)
  try{stopRingtone=startIncomingRingtone()}catch{}
  if('Notification' in window&&Notification.permission==='granted'){
    notification=new Notification('onSIM 新来电',{body:props.call.displayName||props.call.number,tag:`call-${props.call.id}`,requireInteraction:true})
    notification.onclick=()=>window.focus()
  }
}

function stopReminders(){
  if(titleTimer)clearInterval(titleTimer)
  titleTimer=undefined
  stopRingtone?.()
  stopRingtone=undefined
  document.title=originalTitle
  notification?.close()
  notification=undefined
  if(countdownTimer)clearInterval(countdownTimer)
  countdownTimer=undefined
}

async function enableRingtone(){await enableCallAudio();stopRingtone?.();stopRingtone=startIncomingRingtone()}

onMounted(()=>{startReminders();countdownTimer=window.setInterval(()=>now.value=Date.now(),1000)})
onBeforeUnmount(stopReminders)
</script>

<template>
  <div class="incoming-backdrop" role="dialog" aria-modal="true" aria-labelledby="incoming-title">
    <section class="incoming-card">
      <div class="incoming-rings"><span><PhoneCall/></span></div>
      <p class="eyebrow">新来电</p>
      <h2 id="incoming-title">{{call.displayName||call.number||'未知号码'}}</h2>
      <p class="incoming-meta"><Smartphone :size="15"/>{{operator||'手机号码'}} · {{call.filter?.label||'电话来电'}}</p>
      <p v-if="busy==='answer'" class="incoming-progress">正在接听并建立音频连接…</p>
      <p v-else-if="busy==='reject'" class="incoming-progress">正在拒接…</p>
      <p v-else-if="busy==='hold'" class="incoming-progress">正在接通并挂起，请稍候…</p>
      <p v-else-if="busy==='voicemail'" class="incoming-progress">正在转入语音信箱…</p>
      <p v-if="error" class="incoming-error">操作失败：{{error}}</p>
      <p v-if="state.snapshot?.settings.voicemailEnabled&&!busy" class="voicemail-countdown">无人接听时，语音信箱将在 {{voicemailRemaining}} 秒后自动接听</p>
      <button v-if="callAudioState.playback!=='ready'" class="ringtone-enable" @click="enableRingtone">开启来电铃声</button>
      <div class="incoming-actions">
        <button class="incoming-action reject" :disabled="Boolean(busy)" @click="action('hangup')">
          <PhoneOff/><span>拒接</span>
        </button>
        <button class="incoming-action accept" :disabled="Boolean(busy)" @click="action('answer')">
          <PhoneCall/><span>接听</span>
        </button>
      </div>
      <div class="incoming-secondary">
        <button :disabled="Boolean(busy)" @click="action('hold')"><Pause/>挂起</button>
        <button v-if="state.snapshot?.settings.voicemailEnabled" :disabled="Boolean(busy)" @click="action('voicemail')"><Voicemail/>转留言</button>
      </div>
    </section>
  </div>
</template>
