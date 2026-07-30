<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { LayoutDashboard, Phone, History, MessageSquare, ShieldCheck, Settings, LogOut, Radio, WifiOff, Info, Voicemail, Download } from '@lucide/vue'
import { post } from './api'
import { connectEvents, refresh, state } from './state'
import CallPanel from './components/CallPanel.vue'
import IncomingCallModal from './components/IncomingCallModal.vue'
import SignalBars from './components/SignalBars.vue'
import { callAudioState, enableCallAudio, installAudioUnlock, playMessageNotification, requestCallAudioAccess } from './callAudio'
import { initPWA, installPWA, pwa } from './pwa'

const route=useRoute(),router=useRouter()
const publicPage=computed(()=>Boolean(route.meta.public))
const nav=[
  {to:'/',label:'概览',icon:LayoutDashboard},
  {to:'/dial',label:'拨号',icon:Phone},
  {to:'/calls',label:'记录',icon:History},
  {to:'/messages',label:'短信',icon:MessageSquare},
  {to:'/voicemail',label:'语音信箱',icon:Voicemail},
  {to:'/info',label:'信息',icon:Info},
  {to:'/filters',label:'防骚扰',icon:ShieldCheck},
  {to:'/settings',label:'设置',icon:Settings},
]
const mobileNav=[
  nav[0],
  nav[1],
  nav[3],
  nav[2],
  nav[7],
]
async function logout(){await post('/api/v1/auth/logout');router.push('/auth')}
let consoleStarted=false
let messagesInitialized=false
const knownIncomingMessages=new Set<string>()
async function enterConsole(){
  if(consoleStarted)return
  consoleStarted=true
  await refresh()
  connectEvents()
  // Ask immediately on entry, before the first call arrives.
  void requestCallAudioAccess()
}
installAudioUnlock()
initPWA()
onMounted(()=>{if(!publicPage.value)void enterConsole()})
watch(publicPage,value=>{if(!value)void enterConsole()})
watch(()=>state.snapshot?.messages,messages=>{
  if(!messages)return
  const incoming=messages.filter(message=>message.direction==='incoming'&&!message.deleted)
  if(!messagesInitialized){
    incoming.forEach(message=>knownIncomingMessages.add(message.id))
    messagesInitialized=true
    return
  }
  const fresh=incoming.filter(message=>!knownIncomingMessages.has(message.id))
  incoming.forEach(message=>knownIncomingMessages.add(message.id))
  if(!fresh.length)return
  playMessageNotification()
  const newest=fresh[0]
  if('Notification' in window&&Notification.permission==='granted'){
    const notification=new Notification('onSIM 新短信',{
      body:`${newest.number}：${newest.body||'收到一条新短信'}`,
      tag:`message-${newest.id}`,
    })
    notification.onclick=()=>{window.focus();void router.push({path:'/messages',query:{number:newest.number}});notification.close()}
  }
})
</script>

<template>
  <router-view v-if="publicPage"/>
  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand"><span class="brand-mark"><Radio :size="22"/></span><span>onSIM</span></div>
      <div class="device-pill" :class="{offline:state.snapshot?.device.mode!=='online'}">
        <WifiOff v-if="state.snapshot?.device.mode!=='online'" :size="16"/>
        <SignalBars v-else :value="state.snapshot?.device.signal" :registered="state.snapshot?.device.registered" compact/>
        <div><b>{{state.snapshot?.device.mode==='online'?'电话服务在线':'设备离线'}}</b><small>{{state.snapshot?.device.operator || '正在读取运营商'}}</small></div>
      </div>
      <nav><RouterLink v-for="item in nav" :key="item.to" :to="item.to"><component :is="item.icon" :size="19"/><span>{{item.label}}</span><i v-if="item.to==='/messages'&&state.snapshot?.conversations.some(c=>c.unread)">{{state.snapshot?.conversations.reduce((n,c)=>n+c.unread,0)}}</i></RouterLink></nav>
      <button class="logout" @click="logout"><LogOut :size="18"/>退出登录</button>
    </aside>
    <main class="content">
      <header class="mobile-head"><div class="brand"><span class="brand-mark"><Radio :size="18"/></span>onSIM</div><div class="mobile-head-actions"><button v-if="pwa.installable&&!pwa.installed" title="安装 onSIM" aria-label="安装 onSIM" @click="installPWA"><Download/></button><SignalBars :value="state.snapshot?.device.signal" :registered="Boolean(state.snapshot?.device.registered)"/></div></header>
      <button v-if="callAudioState.microphone!=='granted'||callAudioState.playback!=='ready'" class="audio-permission-banner" @click="enableCallAudio">
        <Phone :size="18"/>
        <span><b>启用通话音频与消息铃声</b><small>{{callAudioState.error||'点击允许麦克风、来电铃声和短信提示音'}}</small></span>
      </button>
      <RouterView/>
    </main>
    <nav class="bottom-nav"><RouterLink v-for="item in mobileNav" :key="item.to" :to="item.to"><component :is="item.icon" :size="20"/><span>{{item.label}}</span></RouterLink></nav>
    <IncomingCallModal v-if="state.snapshot?.activeCall?.state==='incoming'" :call="state.snapshot.activeCall" :operator="state.snapshot.device.operator"/>
    <CallPanel v-else-if="state.snapshot?.activeCall" :call="state.snapshot.activeCall"/>
  </div>
</template>
