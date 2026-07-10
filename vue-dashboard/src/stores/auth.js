import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref('')
  const refreshToken = ref('')
  const expiresIn = ref(0)
  const userInfo = ref(null)
  const roleId = ref(0)

  const isLoggedIn = computed(() => !!accessToken.value)
  // 角色细分: 1=superadmin 2=admin 3=user 4=visitor
  const isSuperAdmin  = computed(() => roleId.value === 1)
  const isAdmin       = computed(() => roleId.value >= 1 && roleId.value <= 2)
  const canManageRbac = computed(() => roleId.value === 1)
  const canManageAnno = computed(() => roleId.value >= 1 && roleId.value <= 2)
  const isContentUser = computed(() => roleId.value >= 1 && roleId.value <= 3)  // 可发布文章

  function setTokens(access, refresh, expires) {
    accessToken.value = access
    refreshToken.value = refresh
    expiresIn.value = expires || 86400
  }

  function setUserInfo(info) {
    userInfo.value = info
    roleId.value = (info && info.roleId) || 0
  }

  function logout() {
    accessToken.value = ''
    refreshToken.value = ''
    expiresIn.value = 0
    userInfo.value = null
    roleId.value = 0
  }

  return {
    accessToken, refreshToken, expiresIn, userInfo, roleId,
    isLoggedIn, isSuperAdmin, isAdmin, canManageRbac, canManageAnno, isContentUser,
    setTokens, setUserInfo, logout
  }
}, {
  persist: {
    key: 'auth-login-state',
    storage: localStorage,
    pick: ['accessToken', 'refreshToken', 'expiresIn', 'userInfo', 'roleId']
  }
})
