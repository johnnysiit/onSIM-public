export interface Decision { action:string; label?:string; category?:string; confidence?:number; source?:string; reason?:string }
export interface Call { id:string; version:number; direction:'incoming'|'outgoing'; number:string; displayName?:string; state:string; filter:Decision; startedAt:string; connectedAt?:string; endedAt?:string; endReason?:string; muted:boolean; speakerMuted:boolean; recording:boolean; mediaOwner?:'web'|'sip'|'voicemail'; held:boolean; gatewayId?:string; subscriptionId?:number }
export interface SIPDelivery { status:'queued'|'delivered'|'expired'; attempts:number; nextAttemptAt?:string; expiresAt:string; lastError?:string }
export interface Message { id:string; version:number; conversationId:string; direction:'incoming'|'outgoing'; number:string; body?:string; status:string; unread:boolean; filtered:boolean; deleted:boolean; filter:Decision; sipDelivery?:SIPDelivery; createdAt:string; gatewayId?:string; subscriptionId?:number }
export interface Conversation { id:string; number:string; displayName?:string; lastBody?:string; lastAt:string; unread:number; filtered:boolean }
export interface Rule { id?:string; kind:string; pattern:string; label:string; category:string; action:string; scope:string; enabled:boolean; createdAt?:string }
export interface Settings { smsEnabled:boolean; callsEnabled:boolean; showTestTone:boolean; voicemailEnabled:boolean; voicemailTimeoutSeconds:number; telegramEnabled:boolean; telegramChatId:number; telegramToken?:string; sipEnabled:boolean; providerUrl?:string; providerApiKey?:string; autoBlockCategories:string[]; country:string }
export interface Device { mode:string; gatewayType:string; atConnected:boolean; audioCapable:boolean; simReady:boolean; registered:boolean; voiceRegistered:boolean; operator:string; accessTechnology:string; signal:number; signalDbm:number; telegramOk:boolean; sipStatus:string; sipPendingMessages:number; diskUsedPct:number; degraded:string[]; lastCheckedAt:string }
export interface GatewaySubscription { id:number; simSlot:number; displayName?:string; carrierName?:string; phoneNumber?:string; imei?:string; ready:boolean }
export interface Gateway { id:string;type:string;connected:boolean;transport:string;audioCapable:boolean;adbState?:string;manufacturer?:string;model?:string;androidVersion?:string;buildId?:string;securityPatch?:string;basebandVersion?:string;batteryLevel?:number;batteryCharging?:boolean;subscriptionId?:number;simSlot?:number;imei?:string;imsRegistered?:boolean;volte?:boolean;companionVersion?:string;protocolVersion?:number;audioDownlinkOk?:boolean;audioUplinkOk?:boolean;audioDownlinkFrames?:number;audioUplinkFrames?:number;audioUplinkBytes?:number;lastError?:string;subscriptions?:GatewaySubscription[]}
export interface SystemInfo {
  sim:{ready:boolean;phoneNumber:string;iccid:string;imsi:string}
  network:{registered:boolean;voiceRegistered:boolean;operator:string;accessTechnology:string;signal:number;signalDbm:number}
  modem:{connected:boolean;audioCapable:boolean;manufacturer:string;model:string;imei:string;firmware:string;subVersion:string;qcn:string;volteControl:boolean;atPort:string;audioPort:string}
  gateway:Gateway
  gateways:Gateway[]
  runtime:{version:string;revision:string;buildTime:string;startedAt:string;uptimeSeconds:number}
  lastCheckedAt:string
}
export interface Recording { id:string; callId:string; fileName:string; durationSeconds:number; size:number; sha256:string; createdAt:string; kind?:'call'|'voicemail' }
export interface Snapshot { sequence:number; initialized:boolean; device:Device; activeCall?:Call; calls:Call[]; messages:Message[]; conversations:Conversation[]; rules:Rule[]; recordings:Recording[]; settings:Settings }
