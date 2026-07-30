<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { Phone, MessageSquare, Signal, HardDrive, ArrowUpRight, ArrowDownLeft, ShieldAlert } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { state } from '../state'
import type { Call } from '../types'
const router=useRouter()
const calls=computed(()=>state.snapshot?.calls.slice(0,5)||[])
const unread=computed(()=>state.snapshot?.conversations.reduce((n,c)=>n+c.unread,0)||0)
const degraded=computed(()=>state.snapshot?.device.degraded.map(v=>({
  USB_AUDIO_UNAVAILABLE:'通话音频不可用',
  CELLULAR_NETWORK_UNAVAILABLE:'蜂窝网络未注册',
  CS_VOICE_FALLBACK_UNAVAILABLE:'2G/3G 语音回落不可用',
  MODEM_OFFLINE:'蜂窝模块离线',
  GATEWAY_OFFLINE:'电话网关离线',
  ANDROID_AUDIO_UNAVAILABLE:'Android 通话音频不可用',
}[v]||v)).join(' · ')||'')
const fmt=(v:string)=>new Intl.DateTimeFormat('zh-CN',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}).format(new Date(v))
const callOutcome=(call:Call)=>{
  if(call.connectedAt){
    const end=call.endedAt?new Date(call.endedAt).getTime():Date.now()
    const seconds=Math.max(0,Math.round((end-new Date(call.connectedAt).getTime())/1000))
    return `已通话 ${seconds>=60?`${Math.floor(seconds/60)}分${seconds%60}秒`:`${seconds}秒`}`
  }
  if(call.endReason==='rejected')return '已拒接'
  if(call.direction==='incoming')return '未接来电'
  return call.state==='failed'?'呼叫失败':'未接通'
}
</script>
<template><section>
  <PageHeader eyebrow="控制中心" title="你好，设备状态一览" description="通话、短信与过滤服务的实时状态。"><button class="primary compact" @click="router.push('/dial')"><Phone :size="17"/>拨打电话</button></PageHeader>
  <div v-if="degraded" class="alert"><ShieldAlert/><div><b>功能降级</b><p>{{degraded}}</p></div></div>
  <div class="metrics">
    <article><span class="metric-icon green"><Signal/></span><div><small>{{state.snapshot?.device.gatewayType==='android'?'连接的手机':'电话设备'}}</small><strong>{{state.snapshot?.device.mode==='online'?'在线':'离线'}}</strong><p>{{state.snapshot?.device.audioCapable?'通话语音可用':'正在准备通话语音'}}</p></div></article>
    <article><span class="metric-icon blue"><MessageSquare/></span><div><small>未读短信</small><strong>{{unread}}</strong><p>{{state.snapshot?.messages.length||0}} 条已保存</p></div></article>
    <article><span class="metric-icon amber"><Phone/></span><div><small>通话记录</small><strong>{{state.snapshot?.calls.length||0}}</strong><p>{{state.snapshot?.calls.filter(c=>c.direction==='incoming'&&c.state==='ended').length||0}} 次呼入</p></div></article>
    <article><span class="metric-icon violet"><HardDrive/></span><div><small>存储占用</small><strong>{{Math.round(state.snapshot?.device.diskUsedPct||0)}}%</strong><p>录音永久保留</p></div></article>
  </div>
  <div class="dashboard-grid"><article class="card"><div class="card-head"><div><p class="eyebrow">最近活动</p><h2>通话记录</h2></div><button class="text-btn" @click="router.push('/calls')">查看全部</button></div><div class="activity-list"><div v-for="call in calls" :key="call.id"><span class="avatar">{{call.number.slice(-2)}}</span><div class="grow"><b>{{call.displayName||call.number}}</b><p>{{callOutcome(call)}}</p></div><component :is="call.direction==='outgoing'?ArrowUpRight:ArrowDownLeft" :class="call.direction"/><time>{{fmt(call.startedAt)}}</time></div><p v-if="!calls.length" class="empty">还没有通话记录</p></div></article>
  <article class="card quick"><p class="eyebrow">快捷操作</p><h2>现在要做什么？</h2><button @click="router.push('/dial')"><span class="metric-icon green"><Phone/></span><div><b>拨打电话</b><p>打开拨号键盘</p></div><ArrowUpRight/></button><button @click="router.push('/messages')"><span class="metric-icon blue"><MessageSquare/></span><div><b>发送短信</b><p>进入短信会话</p></div><ArrowUpRight/></button></article></div>
</section></template>
