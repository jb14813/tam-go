export const handlers = {
  async get(url) {
    const res = await fetch(url);
    if (res.ok) {
      const data = await res.json();
      return data
    } else {
      return null
    }
  }
}
