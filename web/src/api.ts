export async function api<T>(path:string,init?:RequestInit):Promise<T>{const response=await fetch('/api/v1'+path,{credentials:'same-origin',headers:{'Content-Type':'application/json',...(init?.headers||{})},...init});if(!response.ok){let message=response.statusText;try{const body=await response.json();message=body.error||message}catch{}const error=new Error(message) as Error&{status:number};error.status=response.status;throw error}if(response.status===204)return undefined as T;return response.json()}
export const post=<T>(path:string,body:unknown)=>api<T>(path,{method:'POST',body:JSON.stringify(body)})
export const put=<T>(path:string,body:unknown)=>api<T>(path,{method:'PUT',body:JSON.stringify(body)})
export const del=(path:string)=>api<void>(path,{method:'DELETE'})
