import { createContext, useContext } from 'react'

// login(key):先向网关校验 key,成功才写会话并放行;logout:清除会话。
export interface AuthApi {
  login: (key: string) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthApi>({
  login: async () => {},
  logout: () => {},
})

export function useAuth(): AuthApi {
  return useContext(AuthContext)
}
