<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { MessageSquarePlus, Send, Trash2, ShieldBan, ArrowLeft, Search, CheckCheck } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { refresh, state } from '../state'
import { del, post } from '../api'
import GatewayRouteSelect from '../components/GatewayRouteSelect.vue'
import { selectedGatewayRoute } from '../gateways'
const selected=ref(''),compose=ref(false),number=ref(''),body=ref(''),q=ref(''),busy=ref(false),readBusy=ref(false),error=ref('')
const route=useRoute()
const errorText:Record<string,string>={
  SMS_SEND_TIMEOUT:'运营商网络未确认短信，模块已自动复位；请稍后重试。',
  SMS_PROMPT_TIMEOUT:'模块未进入短信编辑状态，请稍后重试。',
  MODEM_OFFLINE:'电话设备正在重新连接，请稍后重试。'
}
const conversations=computed(()=>state.snapshot?.conversations.filter(c=>!q.value||c.number.includes(q.value))||[])
const messages=computed(()=>state.snapshot?.messages.filter(m=>m.number===selected.value&&!m.deleted)||[])
const unreadCount=computed(()=>state.snapshot?.conversations.reduce((total,item)=>total+item.unread,0)||0)
watch(messages,()=>nextTick(()=>document.querySelector('.bubbles')?.scrollTo({top:999999,behavior:'smooth'})))
watch(()=>route.query.number,value=>{if(typeof value==='string'&&value){number.value=value;selected.value='';compose.value=true}},{immediate:true})
function choose(n:string){selected.value=n;compose.value=false;for(const m of messages.value.filter(m=>m.unread))post(`/api/v1/messages/${m.id}/read`)}
async function send(){busy.value=true;error.value='';const target=compose.value?number.value:selected.value;const gateway=selectedGatewayRoute.value;try{await post('/api/v1/messages',{Number:target,Body:body.value,GatewayID:gateway?.gatewayId,SubscriptionID:gateway?.subscriptionId});body.value='';selected.value=target;compose.value=false}catch(e:any){error.value=e.code}finally{busy.value=false}}
async function remove(id:string){if(confirm('删除这条短信？'))await del(`/api/v1/messages/${id}`)}
async function block(){await post('/api/v1/rules',{kind:'exact',pattern:selected.value,label:'手动黑名单',category:'manual',action:'block',scope:'sms',enabled:true});}
async function readAll(){if(readBusy.value||!unreadCount.value)return;readBusy.value=true;try{await post('/api/v1/messages/read-all');await refresh()}finally{readBusy.value=false}}
const time=(v:string)=>new Intl.DateTimeFormat('zh-CN',{month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'}).format(new Date(v))
</script>
<template><section class="messages-page"><PageHeader eyebrow="消息" title="短信" description="同一号码的收发短信按会话聚合。"><button class="secondary compact read-all" :disabled="readBusy||!unreadCount" @click="readAll"><CheckCheck :size="17"/>{{readBusy?'处理中…':`全部已读${unreadCount?` (${unreadCount})`:''}`}}</button><button class="primary compact" @click="compose=true;selected=''"><MessageSquarePlus :size="17"/>新短信</button></PageHeader><div class="messenger card">
  <aside class="threads" :class="{mobileHidden:selected||compose}"><label class="search"><Search/><input v-model="q" placeholder="搜索会话"/></label><button v-for="c in conversations" :key="c.id" @click="choose(c.number)" :class="{active:selected===c.number}"><span class="avatar">{{c.number.slice(-2)}}</span><div><b>{{c.displayName||c.number}}</b><p>{{c.lastBody||'已删除'}}</p></div><span><time>{{time(c.lastAt)}}</time><i v-if="c.unread">{{c.unread}}</i></span></button><p v-if="!conversations.length" class="empty">暂无短信会话</p></aside>
  <main class="chat" :class="{mobileHidden:!selected&&!compose}"><header v-if="selected"><button class="mobile-back" @click="selected=''"><ArrowLeft/></button><span class="avatar">{{selected.slice(-2)}}</span><div><b>{{selected}}</b><small>短信会话</small></div><button class="icon-btn" title="加入黑名单" @click="block"><ShieldBan/></button></header><header v-else-if="compose"><button class="mobile-back" @click="compose=false"><ArrowLeft/></button><div><b>发送新短信</b><small>输入收件人号码</small></div></header>
    <div v-if="selected" class="bubbles"><div v-for="m in messages" :key="m.id" class="bubble-wrap" :class="m.direction"><div class="bubble"><p>{{m.body}}</p><footer>{{time(m.createdAt)}} · {{m.status}}<button @click="remove(m.id)"><Trash2 :size="13"/></button></footer></div></div><p v-if="!messages.length" class="empty">开始这段会话</p></div>
    <div v-else-if="compose" class="compose-target"><label>收件人<input v-model="number" inputmode="tel" placeholder="+86 138 0000 0000"/></label></div>
    <div v-else class="chat-empty"><MessageSquarePlus/><h3>选择一个会话</h3><p>或发送一条新短信</p></div>
    <GatewayRouteSelect v-if="selected||compose"/>
    <form v-if="selected||compose" class="composer" @submit.prevent="send"><textarea v-model="body" maxlength="1000" placeholder="输入短信内容…" required/><button class="send" :disabled="busy||!body||(!selected&&!number)||!selectedGatewayRoute?.connected"><Send/></button><span v-if="error" class="form-error">{{errorText[error]||error}}</span></form>
  </main>
</div></section></template>
