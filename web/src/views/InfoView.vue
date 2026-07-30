<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Activity, Cpu, IdCard, RadioTower, RefreshCw } from '@lucide/vue'
import { api } from '../api'
import SignalBars from '../components/SignalBars.vue'
import PageHeader from '../components/PageHeader.vue'
import type { SystemInfo } from '../types'
const info=ref<SystemInfo|null>(null),error=ref(''),loading=ref(false)
let timer:number|undefined
async function load(){loading.value=true;try{info.value=await api<SystemInfo>('/api/v1/info');error.value=''}catch(e:any){error.value=e.code||e.message}finally{loading.value=false}}
const show=(value?:string)=>value||'模块未提供'
const checked=computed(()=>info.value?.lastCheckedAt?new Intl.DateTimeFormat('zh-CN',{dateStyle:'medium',timeStyle:'medium'}).format(new Date(info.value.lastCheckedAt)):'尚未检查')
const uptime=computed(()=>{let seconds=info.value?.runtime.uptimeSeconds||0;const days=Math.floor(seconds/86400);seconds%=86400;const hours=Math.floor(seconds/3600);seconds%=3600;const minutes=Math.floor(seconds/60);return [days&&`${days} 天`,(hours||days)&&`${hours} 小时`,`${minutes} 分钟`].filter(Boolean).join(' ')})
onMounted(()=>{load();timer=window.setInterval(load,60000)})
onBeforeUnmount(()=>{if(timer)clearInterval(timer)})
</script>
<template><section>
  <PageHeader eyebrow="设备详情" title="信息" :description="`最近检查：${checked}`"><button class="secondary compact" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{spin:loading}"/>刷新</button></PageHeader>
  <p v-if="error" class="alert">{{error}}</p>
  <div v-if="info" class="info-grid">
    <article class="card info-card"><div class="info-title"><span class="metric-icon green"><IdCard/></span><div><p class="eyebrow">SIM</p><h2>连接的 SIM 卡</h2></div><span class="tag" :class="{active:info.sim.ready}">{{info.sim.ready?'已就绪':'未就绪'}}</span></div><dl><div><dt>手机号</dt><dd>{{show(info.sim.phoneNumber)}}</dd></div><div><dt>ICCID</dt><dd>{{show(info.sim.iccid)}}</dd></div><div><dt>IMSI</dt><dd>{{show(info.sim.imsi)}}</dd></div></dl></article>
    <article class="card info-card"><div class="info-title"><span class="metric-icon blue"><RadioTower/></span><div><p class="eyebrow">蜂窝网络</p><h2>{{show(info.network.operator)}}</h2></div><SignalBars :value="info.network.signal" :registered="info.network.registered"/></div><dl><div><dt>LTE / 数据注册</dt><dd>{{info.network.registered?'已注册':'未注册'}}</dd></div><div><dt>2G/3G 语音回落</dt><dd>{{info.network.voiceRegistered?'已注册':'不可用（不代表 VoLTE）'}}</dd></div><div><dt>接入制式</dt><dd>{{show(info.network.accessTechnology)}}</dd></div><div><dt>信号强度</dt><dd>{{info.network.signal<0||info.network.signal===99?'未知':`CSQ ${info.network.signal} · ${info.network.signalDbm} dBm`}}</dd></div></dl></article>
    <article v-for="gateway in (info.gateways?.length?info.gateways:[info.gateway]).filter(item=>item.type==='android')" :key="gateway.id||gateway.model" class="card info-card"><div class="info-title"><span class="metric-icon amber"><Cpu/></span><div><p class="eyebrow">已连接手机</p><h2>{{show(gateway.model)}}</h2></div><span class="status-dot" :class="{bad:!gateway.connected}"/></div><dl><div><dt>连接状态</dt><dd>{{gateway.connected?'已连接':'未连接'}}</dd></div><div><dt>系统版本</dt><dd>Android {{show(gateway.androidVersion)}} · {{show(gateway.buildId)}}</dd></div><div><dt>基带版本</dt><dd>{{show(gateway.basebandVersion)}}</dd></div><div v-for="sim in gateway.subscriptions" :key="sim.id"><dt>电话卡 {{sim.simSlot+1}}</dt><dd>{{sim.phoneNumber||sim.displayName||'号码未知'}} · {{sim.carrierName||'运营商未知'}}</dd></div><div><dt>电池</dt><dd>{{gateway.batteryLevel??'未知'}}% {{gateway.batteryCharging?'· 充电中':''}}</dd></div><div><dt>连接服务版本</dt><dd>{{show(gateway.companionVersion)}}</dd></div><div><dt>通话语音</dt><dd :class="{danger:!gateway.audioUplinkOk||!gateway.audioDownlinkOk}">{{gateway.audioUplinkOk&&gateway.audioDownlinkOk?'可用':'不可用'}}</dd></div><div v-if="gateway.lastError"><dt>最近错误</dt><dd class="danger">{{gateway.lastError}}</dd></div></dl></article>
    <article v-if="info.gateway.type!=='android'" class="card info-card"><div class="info-title"><span class="metric-icon amber"><Cpu/></span><div><p class="eyebrow">电话设备</p><h2>{{show(info.modem.model)}}</h2></div><span class="status-dot" :class="{bad:!info.modem.connected}"/></div><dl><div><dt>制造商</dt><dd>{{show(info.modem.manufacturer)}}</dd></div><div><dt>IMEI</dt><dd>{{show(info.modem.imei)}}</dd></div><div><dt>基带 / 固件</dt><dd>{{show(info.modem.firmware)}}</dd></div><div><dt>固件子版本</dt><dd>{{show(info.modem.subVersion)}}</dd></div><div><dt>运营商配置</dt><dd>{{show(info.modem.qcn)}}</dd></div><div><dt>VoLTE</dt><dd :class="{danger:!info.modem.volteControl}">{{info.modem.volteControl?'可用':'不可用'}}</dd></div><div><dt>通话语音</dt><dd>{{info.modem.audioCapable?'可用':'不可用'}}</dd></div></dl></article>
    <article class="card info-card"><div class="info-title"><span class="metric-icon violet"><Activity/></span><div><p class="eyebrow">运行环境</p><h2>onSIM {{info.runtime.version}}</h2></div></div><dl><div><dt>运行时长</dt><dd>{{uptime}}</dd></div><div><dt>启动时间</dt><dd>{{new Date(info.runtime.startedAt).toLocaleString('zh-CN')}}</dd></div><div><dt>Git revision</dt><dd>{{info.runtime.revision}}</dd></div><div><dt>构建时间</dt><dd>{{info.runtime.buildTime}}</dd></div></dl></article>
  </div>
</section></template>
