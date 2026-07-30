<script setup lang="ts">
import { ref } from 'vue'
import { ShieldCheck, Plus, Trash2, CheckCircle2, Ban, Tag } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { state } from '../state'
import { del, post } from '../api'
import type { Rule } from '../types'
const show=ref(false),error=ref(''),form=ref<Rule>({kind:'exact',pattern:'',label:'',category:'manual',action:'block',scope:'both',enabled:true})
async function save(){try{await post('/api/v1/rules',form.value);show.value=false;form.value={kind:'exact',pattern:'',label:'',category:'manual',action:'block',scope:'both',enabled:true}}catch(e:any){error.value=e.code}}
async function remove(id?:string){if(id&&confirm('删除这条规则？'))await del(`/api/v1/rules/${id}`)}
const icon=(action:string)=>action==='allow'?CheckCircle2:action==='block'?Ban:Tag
</script>
<template><section><PageHeader eyebrow="防骚扰" title="过滤规则" description="白名单优先；手动黑名单必定拦截。"><button class="primary compact" @click="show=true"><Plus :size="17"/>添加规则</button></PageHeader>
<div class="filter-summary"><article class="card"><ShieldCheck/><div><b>保护已开启</b><p>诈骗与保险推销自动拦截</p></div></article><article class="card"><strong>{{state.snapshot?.rules.filter(r=>r.action==='block').length||0}}</strong><p>拦截规则</p></article><article class="card"><strong>{{state.snapshot?.rules.filter(r=>r.action==='allow').length||0}}</strong><p>白名单规则</p></article></div>
<article class="card table-card"><div class="rule-list"><div v-for="r in state.snapshot?.rules" :key="r.id"><span class="rule-icon" :class="r.action"><component :is="icon(r.action)"/></span><div class="grow"><b>{{r.label||r.pattern}}</b><p>{{r.pattern}} · {{r.scope==='both'?'电话与短信':r.scope}}</p></div><span class="tag">{{r.category}}</span><button class="icon-btn" @click="remove(r.id)"><Trash2/></button></div><p v-if="!state.snapshot?.rules.length" class="empty">尚未添加本地规则</p></div></article>
<div v-if="show" class="modal-backdrop" @click.self="show=false"><form class="modal" @submit.prevent="save"><p class="eyebrow">新规则</p><h2>添加过滤规则</h2><label>匹配方式<select v-model="form.kind"><option value="exact">完整号码</option><option value="prefix">号码前缀</option><option value="keyword">短信关键词</option><option value="regex">正则表达式</option></select></label><label>匹配内容<input v-model="form.pattern" required placeholder="+86138…"/></label><label>显示标签<input v-model="form.label" required placeholder="例如：保险推销"/></label><div class="form-grid"><label>动作<select v-model="form.action"><option value="block">拦截</option><option value="allow">白名单</option><option value="label">仅标记</option></select></label><label>范围<select v-model="form.scope"><option value="both">电话与短信</option><option value="call">仅电话</option><option value="sms">仅短信</option></select></label></div><p v-if="error" class="form-error">{{error}}</p><div class="modal-actions"><button type="button" class="secondary" @click="show=false">取消</button><button class="primary">保存规则</button></div></form></div>
</section></template>
