// ProxyPilot core 的 /api/status 响应体里用于身份识别的关键字段。
// 判定时字段必须齐全且类型正确，才认定为 ProxyPilot core。
export interface CoreStatusPayload {
  code: number
  msg: string
  data: {
    version: string
  }
}

// isProxyPilotStatus 判断一个 /api/status 响应体是否来自 ProxyPilot core。
export function isProxyPilotStatus(body: unknown): body is CoreStatusPayload {
  if (typeof body !== 'object' || body === null) {
    return false
  }
  const payload = body as Partial<CoreStatusPayload>
  return payload.code === 0 && typeof payload.data?.version === 'string'
}
