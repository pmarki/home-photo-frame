import { ref } from 'vue'

const COOKIE_NAME = 'user'

function readCookie() {
  const m = document.cookie.match(/(?:^|;\s*)user=([^;]*)/)
  return m ? decodeURIComponent(m[1]) : ''
}

function writeCookie(id) {
  if (id) {
    document.cookie = `${COOKIE_NAME}=${encodeURIComponent(id)}; path=/; max-age=${365 * 24 * 60 * 60}; samesite=lax`
  } else {
    document.cookie = `${COOKIE_NAME}=; path=/; max-age=0; samesite=lax`
  }
}

const userId = ref(readCookie())

export function useUser() {
  function setUser(id) {
    userId.value = id || ''
    writeCookie(userId.value)
  }
  function clearUser() {
    userId.value = ''
    writeCookie('')
  }
  return { userId, setUser, clearUser }
}
