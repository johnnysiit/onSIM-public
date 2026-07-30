<script setup lang="ts">
import { onMounted } from 'vue'
import { Smartphone, RefreshCw } from '@lucide/vue'
import { gatewayState, refreshGatewayRoutes, selectGatewayRoute } from '../gateways'
onMounted(()=>{if(!gatewayState.routes.length)void refreshGatewayRoutes()})
</script>
<template>
  <label class="gateway-route-select">
    <Smartphone :size="18"/>
    <span><b>使用号码</b><small>{{gatewayState.routes.find(route=>route.key===gatewayState.selectedKey)?.detail||'正在读取可用号码…'}}</small></span>
    <select :value="gatewayState.selectedKey" :disabled="gatewayState.loading" @change="selectGatewayRoute(($event.target as HTMLSelectElement).value)">
      <option v-for="route in gatewayState.routes" :key="route.key" :value="route.key" :disabled="!route.connected">{{route.label}}{{route.connected?'':'（离线）'}}</option>
    </select>
    <button type="button" class="icon-btn" title="刷新号码" @click="refreshGatewayRoutes"><RefreshCw :class="{spin:gatewayState.loading}"/></button>
  </label>
</template>
