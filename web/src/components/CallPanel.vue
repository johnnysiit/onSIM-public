<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Mic, MicOff, PhoneOff, Grid3X3, X, Circle, Square, PhoneCall, AudioLines, Volume2, Pause, Play, Voicemail } from '@lucide/vue'
import type { Call } from '../types'
import { post } from '../api'
import { connectCallMedia, type CallMediaSession, type CallMediaStatus } from '../media'
import { playDTMFKey } from '../callAudio'
import { state } from '../state'
const props=defineProps<{call:Call}>()
const keypad=ref(false),busy=ref(false),mediaError=ref(''),mediaSession=ref<CallMediaSession>(),mediaConnecting=ref(false),tonePlaying=ref(false),toneNotice=ref(''),dtmfHistory=ref(''),now=ref(Date.now())
const media=ref<CallMediaStatus>({phase:'negotiating',micLevel:0,packetsSent:0,bytesSent:0})
let clock:number|undefined,retryTimer:number|undefined,retryAttempt=0,destroyed=false
const keys=['1','2','3','4','5','6','7','8','9','*','0','#']
async function act(name:string,body:any={}){busy.value=true;try{await post(`/api/v1/calls/${props.call.id}/${name}`,body)}finally{busy.value=false}}
function scheduleMediaRetry(){
  if(destroyed||props.call.state!=='active'||props.call.held||props.call.mediaOwner==='sip'||props.call.mediaOwner==='voicemail'||retryTimer)return
  retryAttempt++
  retryTimer=window.setTimeout(()=>{retryTimer=undefined;void startMedia()},Math.min(3000,500+retryAttempt*350))
}
async function startMedia(){
  if(destroyed||props.call.state!=='active'||props.call.held||mediaSession.value||mediaConnecting.value||props.call.mediaOwner==='sip'||props.call.mediaOwner==='voicemail')return
  mediaConnecting.value=true
  mediaError.value=''
  media.value={...media.value,phase:'negotiating',error:undefined}
  try{
    const session=await connectCallMedia(props.call.id,false,value=>{
      media.value=value
      if(value.phase==='bridged'){retryAttempt=0;mediaError.value=''}
      if(value.phase==='failed'&&mediaSession.value){
        const failed=mediaSession.value
        mediaSession.value=undefined
        failed.disconnect()
        scheduleMediaRetry()
      }
    })
    mediaSession.value=session
    if(media.value.phase==='failed'){
      mediaSession.value=undefined
      session.disconnect()
      scheduleMediaRetry()
    }
  }catch(e:any){
    mediaError.value=e.code||e.message
    media.value={...media.value,phase:'failed',error:mediaError.value}
    scheduleMediaRetry()
  }finally{mediaConnecting.value=false}
}
async function testTone(){if(!mediaSession.value||tonePlaying.value)return;tonePlaying.value=true;toneNotice.value='正在向对方发送 2 秒测试音…';try{await mediaSession.value.playTestTone();toneNotice.value='测试音已发送，请让对方确认是否听到'}catch(e:any){toneNotice.value=`测试音失败：${e.code||e.message}`}finally{tonePlaying.value=false}}
async function sendDTMF(key:string){
  playDTMFKey(key,150)
  dtmfHistory.value=(dtmfHistory.value+key).slice(-48)
  try{await post(`/api/v1/calls/${props.call.id}/dtmf`,{key})}
  catch(e:any){mediaError.value=e.code||e.message}
}
async function record(){await act(props.call.recording?'recording/stop':'recording/start')}
const duration=computed(()=>{const start=new Date(props.call.connectedAt||props.call.startedAt).getTime(),seconds=Math.max(0,Math.floor((now.value-start)/1000));return `${String(Math.floor(seconds/60)).padStart(2,'0')}:${String(seconds%60).padStart(2,'0')}`})
async function holdOrResume(){const wasHeld=props.call.held;await act(wasHeld?'resume':'hold');if(wasHeld)setTimeout(startMedia,100)}
async function toVoicemail(){if(confirm('将当前挂起的通话转入语音信箱？'))await act('voicemail')}
const stateLabel=computed(()=>props.call.held?'通话已挂起 · 对方正在等待':props.call.state==='active'?'已接通':props.call.state==='alerting'?'正在接通…':'正在拨号…')
onMounted(()=>{startMedia();clock=window.setInterval(()=>now.value=Date.now(),1000)})
watch(()=>[props.call.state,props.call.held],startMedia)
onBeforeUnmount(()=>{destroyed=true;if(retryTimer)clearTimeout(retryTimer);mediaSession.value?.disconnect();if(clock)clearInterval(clock)})
</script>
<template>
  <aside class="call-panel">
    <div class="call-glow"/>
    <div class="active-call-icon"><PhoneCall/></div>
    <p class="eyebrow">{{call.state==='active'?'通话中':'电话'}}</p>
    <h2>{{call.displayName||call.number}}</h2>
    <p>{{stateLabel}}<b v-if="call.state==='active'" class="call-duration">{{duration}}</b></p>
    <div v-if="call.held" class="media-health held"><Pause :size="18"/><div><b>通话已挂起</b><small>语音和录音已暂停，对方正在等待</small></div></div>
    <div v-else-if="call.mediaOwner!=='sip'&&call.mediaOwner!=='voicemail'" class="media-health" :class="media.phase">
      <AudioLines :size="18"/>
      <div><b>{{media.phase==='bridged'?'通话语音已连接':media.phase==='failed'||media.phase==='timeout'?'通话语音连接异常':media.phase==='recovering'?'正在恢复通话语音':media.phase==='waiting_for_permission'?'等待麦克风权限':'正在连接通话语音'}}</b>
      <small>麦克风上行 {{media.packetsSent}} 包 · {{media.bytesSent}} 字节</small></div>
      <span class="mic-meter"><i :style="{width:`${media.micLevel}%`}"/></span>
    </div>
    <p v-if="call.mediaOwner==='sip'" class="media-error">音频由 Groundwire 接管</p>
    <p v-if="call.mediaOwner==='voicemail'" class="voicemail-active">语音信箱正在播放提示并录制来电者留言</p>
    <button v-if="call.mediaOwner!=='sip'&&call.mediaOwner!=='voicemail'&&state.snapshot?.settings.showTestTone" class="tone-test" :disabled="media.phase!=='bridged'||tonePlaying" @click="testTone"><Volume2 :size="17"/>{{tonePlaying?'正在发送测试音':'向对方发送测试音'}}</button>
    <p v-if="toneNotice" class="tone-notice">{{toneNotice}}</p>
    <div v-if="keypad&&call.mediaOwner!=='voicemail'" class="call-keypad">
      <div class="dtmf-history"><small>按键记录</small><b>{{dtmfHistory||'尚未输入'}}</b><button v-if="dtmfHistory" type="button" @click="dtmfHistory=''">清除</button></div>
      <div class="mini-keypad"><button v-for="k in keys" :key="k" @click="sendDTMF(k)">{{k}}</button></div>
    </div>
    <p v-if="mediaError||media.error" class="media-error">音频：{{mediaError||media.error}}</p>
    <div class="call-actions">
      <button v-if="call.mediaOwner==='web'" class="call-control" :class="{held:call.held}" :disabled="busy" @click="holdOrResume"><span><Play v-if="call.held"/><Pause v-else/></span><small>{{call.held?'恢复':'挂起'}}</small></button>
      <button v-if="call.held&&state.snapshot?.settings.voicemailEnabled" class="call-control" :disabled="busy" @click="toVoicemail"><span><Voicemail/></span><small>转留言</small></button>
      <button v-if="call.mediaOwner!=='voicemail'" class="call-control" :disabled="call.mediaOwner==='sip'" @click="act('mute',{muted:!call.muted})"><span><MicOff v-if="call.muted"/><Mic v-else/></span><small>{{call.muted?'取消静音':'静音'}}</small></button>
      <button v-if="call.mediaOwner!=='voicemail'" class="call-control" @click="keypad=!keypad"><span><X v-if="keypad"/><Grid3X3 v-else/></span><small>键盘</small></button>
      <button v-if="call.mediaOwner!=='voicemail'" class="call-control" :class="{recording:call.recording}" @click="record"><span><Square v-if="call.recording"/><Circle v-else/></span><small>{{call.recording?'停止录音':'录音'}}</small></button>
      <button class="call-control hangup" :disabled="busy" @click="act('hangup')"><span><PhoneOff/></span><small>挂断</small></button>
    </div>
  </aside>
</template>
