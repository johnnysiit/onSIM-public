import { computed, reactive } from 'vue'
import { api } from './api'
import type { SystemInfo } from './types'

export interface GatewayRoute {
  key:string
  gatewayId:string
  subscriptionId:number
  label:string
  detail:string
  connected:boolean
}

export const gatewayState=reactive<{routes:GatewayRoute[];selectedKey:string;loading:boolean}>({
  routes:[],selectedKey:localStorage.getItem('onsim.gatewayRoute')||'',loading:false
})

export const selectedGatewayRoute=computed(()=>{
  const selected=gatewayState.routes.find(route=>route.key===gatewayState.selectedKey)
  return selected||gatewayState.routes.find(route=>route.connected)||gatewayState.routes[0]
})

export function selectGatewayRoute(key:string){
  gatewayState.selectedKey=key
  localStorage.setItem('onsim.gatewayRoute',key)
}

export async function refreshGatewayRoutes(){
  gatewayState.loading=true
  try{
    const info=await api<SystemInfo>('/api/v1/info')
    const gateways=info.gateways?.length?info.gateways:[info.gateway]
    const routes:GatewayRoute[]=[]
    for(const gateway of gateways){
      const subscriptions=gateway.subscriptions||[]
      if(subscriptions.length){
        for(const subscription of subscriptions)routes.push({
          key:`${gateway.id}:${subscription.id}`,gatewayId:gateway.id,subscriptionId:subscription.id,
          label:subscription.phoneNumber||subscription.displayName||`SIM ${subscription.simSlot+1}`,
          detail:`${gateway.model||gateway.id} · 卡槽 ${subscription.simSlot+1} · ${subscription.carrierName||'未知运营商'}`,
          connected:gateway.connected&&subscription.ready,
        })
      }else routes.push({
        key:`${gateway.id||'default'}:${gateway.subscriptionId||0}`,gatewayId:gateway.id||'',
        subscriptionId:gateway.subscriptionId||0,label:info.sim.phoneNumber||gateway.model||'默认号码',
        detail:`${gateway.model||gateway.type} · ${info.network.operator||'默认 SIM'}`,connected:gateway.connected,
      })
    }
    gatewayState.routes=routes
    if(!routes.some(route=>route.key===gatewayState.selectedKey)){
      selectGatewayRoute(routes.find(route=>route.connected)?.key||routes[0]?.key||'')
    }
  }finally{gatewayState.loading=false}
}
