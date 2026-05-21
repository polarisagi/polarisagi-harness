// Polaris Harness — Rust substrate crate
// 包含 Cedar 策略引擎 FFI 接口与 SIMD 向量运算路径。
// 架构文档: docs/arch/M11-Policy-Safety.md §3
//
// FFI 设计约束:
//   - 所有跨边界内存必须显式 free（cedar_free_string）
//   - 字符串参数：Go 侧保证 NUL-terminated UTF-8，Rust 侧不信任
//   - panic 不可越过 FFI 边界 —— 所有函数捕获 panic 转为错误码
//   - thread-safety: PolicyStore 通过 Arc<RwLock<>> 保护并发读写

#![allow(clippy::missing_safety_doc)]

use std::ffi::{CStr, CString};
use std::os::raw::{c_char, c_int};
use std::panic;
use std::sync::{Arc, OnceLock, RwLock};

use cedar_policy::{Authorizer, Context, Decision, Entities, EntityUid, PolicySet, Request};

// ─── 全局 PolicyStore ──────────────────────────────────────────────────────────

/// 全局 PolicySet，保护并发读写。
/// OnceLock 确保初始化只发生一次（对应 Go 侧 `sync.Once` 初始化语义）。
static POLICY_STORE: OnceLock<Arc<RwLock<PolicySet>>> = OnceLock::new();

fn policy_store() -> Arc<RwLock<PolicySet>> {
    POLICY_STORE
        .get_or_init(|| Arc::new(RwLock::new(PolicySet::new())))
        .clone()
}

// ─── FFI 错误码 ────────────────────────────────────────────────────────────────

/// 评估结果
const CEDAR_ALLOW: c_int = 0;
const CEDAR_DENY: c_int = 1;
const CEDAR_ERR_PARSE: c_int = -1; // 策略解析失败
const CEDAR_ERR_CONTEXT: c_int = -2; // Context/Entities 构造失败
const CEDAR_ERR_INTERNAL: c_int = -3; // panic 或锁中毒
const CEDAR_ERR_UTF8: c_int = -4; // 非法 UTF-8 输入

// ─── ABI 版本协议 ──────────────────────────────────────────────────────────────
// 设计依据: docs/arch/decisions/ADR-0011-cgo-to-purego-migration.md
// Go 侧加载 dylib 后立即调用 substrate_abi_version() 验证 major 匹配；
// major 不匹配 → panic（防 ABI silent drift）。
// 升 major: 破坏性变更（删/改导出函数签名）；升 minor: 加法变更（新增导出函数）。

/// ABI 主版本号：破坏性变更时递增。
const SUBSTRATE_ABI_MAJOR: u16 = 1;

/// ABI 次版本号：加法变更时递增。
const SUBSTRATE_ABI_MINOR: u16 = 0;

/// 返回当前 ABI 版本（高 16 位 major | 低 16 位 minor）。
/// Go 侧用 `(version >> 16) & 0xFFFF` 提取 major。
#[no_mangle]
pub extern "C" fn substrate_abi_version() -> u32 {
    ((SUBSTRATE_ABI_MAJOR as u32) << 16) | (SUBSTRATE_ABI_MINOR as u32)
}

// ─── cedar_load_policies ───────────────────────────────────────────────────────

/// 从 Cedar 策略文本（NUL-terminated UTF-8）加载/替换全局 PolicySet。
/// 返回 0 表示成功，负数表示错误（错误详情通过 out_err 返回）。
/// out_err 由 Rust 分配，调用方须调用 cedar_free_string 释放。
///
/// # Safety
/// policies_text 必须是有效的 NUL-terminated C 字符串，caller 负责生命周期。
#[no_mangle]
pub unsafe extern "C" fn cedar_load_policies(
    policies_text: *const c_char,
    out_err: *mut *mut c_char,
) -> c_int {
    let result = panic::catch_unwind(|| -> c_int {
        // 解析输入字符串
        let text = match unsafe_cstr_to_str(policies_text) {
            Ok(s) => s,
            Err(_) => {
                write_err(out_err, "invalid UTF-8 in policy text");
                return CEDAR_ERR_UTF8;
            }
        };

        // 解析 PolicySet
        let new_set = match text.parse::<PolicySet>() {
            Ok(ps) => ps,
            Err(e) => {
                write_err(out_err, &format!("policy parse error: {e}"));
                return CEDAR_ERR_PARSE;
            }
        };

        // 写入全局 PolicyStore
        let store = policy_store();
        // 显式绑定 write guard，防止临时值在 match 结束前 drop（E0597）
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(e) => {
                write_err(out_err, &format!("lock poisoned: {e}"));
                return CEDAR_ERR_INTERNAL;
            }
        };
        *guard = new_set;
        write_err(out_err, "");
        0
    });

    match result {
        Ok(code) => code,
        Err(_) => {
            write_err(out_err, "panic in cedar_load_policies");
            CEDAR_ERR_INTERNAL
        }
    }
}

// ─── cedar_evaluate ────────────────────────────────────────────────────────────

/// 评估单次策略请求。
/// 参数均为 NUL-terminated UTF-8 C 字符串:
///   principal: Cedar EntityUID 格式，例如 `Agent::"agent-42"`
///   action:    Cedar EntityUID 格式，例如 `Action::"infer"`
///   resource:  Cedar EntityUID 格式，例如 `Resource::"llm_api"`
///   context_json: JSON 对象，例如 `{"trust_level": 3, "capability_token_valid": true}`
///
/// 返回 0(ALLOW) / 1(DENY) / 负数(错误)。
/// out_reason 由 Rust 分配，调用方须调用 cedar_free_string 释放。
///
/// # Safety
/// 所有 *const c_char 参数须为有效 NUL-terminated C 字符串。
#[no_mangle]
pub unsafe extern "C" fn cedar_evaluate(
    principal: *const c_char,
    action: *const c_char,
    resource: *const c_char,
    context_json: *const c_char,
    out_reason: *mut *mut c_char,
) -> c_int {
    let result = panic::catch_unwind(|| -> c_int {
        // 解析四个输入参数
        let p_str = match unsafe_cstr_to_str(principal) {
            Ok(s) => s,
            Err(_) => {
                write_err(out_reason, "invalid UTF-8: principal");
                return CEDAR_ERR_UTF8;
            }
        };
        let a_str = match unsafe_cstr_to_str(action) {
            Ok(s) => s,
            Err(_) => {
                write_err(out_reason, "invalid UTF-8: action");
                return CEDAR_ERR_UTF8;
            }
        };
        let r_str = match unsafe_cstr_to_str(resource) {
            Ok(s) => s,
            Err(_) => {
                write_err(out_reason, "invalid UTF-8: resource");
                return CEDAR_ERR_UTF8;
            }
        };
        let ctx_str = match unsafe_cstr_to_str(context_json) {
            Ok(s) => s,
            Err(_) => {
                write_err(out_reason, "invalid UTF-8: context_json");
                return CEDAR_ERR_UTF8;
            }
        };

        // 构造 EntityUID
        let p_uid = match p_str.parse::<EntityUid>() {
            Ok(u) => u,
            Err(e) => {
                write_err(out_reason, &format!("principal parse: {e}"));
                return CEDAR_ERR_CONTEXT;
            }
        };
        let a_uid = match a_str.parse::<EntityUid>() {
            Ok(u) => u,
            Err(e) => {
                write_err(out_reason, &format!("action parse: {e}"));
                return CEDAR_ERR_CONTEXT;
            }
        };
        let r_uid = match r_str.parse::<EntityUid>() {
            Ok(u) => u,
            Err(e) => {
                write_err(out_reason, &format!("resource parse: {e}"));
                return CEDAR_ERR_CONTEXT;
            }
        };

        // 构造 Context（从 JSON）
        let ctx = match Context::from_json_str(ctx_str, None) {
            Ok(c) => c,
            Err(e) => {
                write_err(out_reason, &format!("context parse: {e}"));
                return CEDAR_ERR_CONTEXT;
            }
        };

        let store = policy_store();
        // 显式绑定 guard，防止 store 在 guard 使用前 drop（E0597）
        let guard = match store.read() {
            Ok(g) => g,
            Err(e) => {
                write_err(out_reason, &format!("lock poisoned: {e}"));
                return CEDAR_ERR_INTERNAL;
            }
        };
        let policy_set: &PolicySet = &guard;

        let request = Request::new(p_uid, a_uid, r_uid, ctx, None)
            .expect("Request::new should not fail with validated EntityUIDs");

        let authorizer = Authorizer::new();
        // 空 Entities —— 实体属性通过 context_json 传递（MVP 简化）
        let entities = Entities::empty();
        let response = authorizer.is_authorized(&request, policy_set, &entities);

        let (code, reason) = match response.decision() {
            Decision::Allow => (CEDAR_ALLOW, "allow"),
            Decision::Deny => (CEDAR_DENY, "deny"),
        };
        write_err(out_reason, reason);
        code
    });

    match result {
        Ok(code) => code,
        Err(_) => {
            write_err(out_reason, "panic in cedar_evaluate");
            CEDAR_ERR_INTERNAL
        }
    }
}

// ─── cedar_policy_count ────────────────────────────────────────────────────────

/// 返回当前 PolicySet 中的策略数量。
/// 用于健康检查和热更新验证。
#[no_mangle]
pub extern "C" fn cedar_policy_count() -> c_int {
    let store = policy_store();
    let guard = match store.read() {
        Ok(g) => g,
        Err(_) => return -1,
    };
    // 显式绑定避免临时 guard 在 match 结束时 drop（E0597）
    let count = guard.policies().count() as c_int;
    count
}

// ─── cedar_free_string ─────────────────────────────────────────────────────────

/// 释放由 cedar_* 函数分配的 C 字符串。
/// 必须与 cedar_load_policies/cedar_evaluate 的 out_err/out_reason 配对调用。
///
/// # Safety
/// ptr 须为 cedar_* 函数分配的指针，或 null。
#[no_mangle]
pub unsafe extern "C" fn cedar_free_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        // 重新构造 CString 以正确释放
        unsafe { drop(CString::from_raw(ptr)) };
    }
}

// ─── 内部工具函数 ──────────────────────────────────────────────────────────────

/// 将 C 字符串指针安全转为 &str（非拷贝，lifetime 绑定到原始指针生命周期）。
unsafe fn unsafe_cstr_to_str<'a>(ptr: *const c_char) -> Result<&'a str, std::str::Utf8Error> {
    if ptr.is_null() {
        return Ok("");
    }
    unsafe { CStr::from_ptr(ptr) }.to_str()
}

/// 写入 out 指针处的错误字符串（调用方须用 cedar_free_string 释放）。
fn write_err(out: *mut *mut c_char, msg: &str) {
    if out.is_null() {
        return;
    }
    match CString::new(msg) {
        Ok(cs) => unsafe { *out = cs.into_raw() },
        Err(_) => {
            // msg 含 NUL 字节，写入截断版本
            let safe = msg.replace('\0', "?");
            if let Ok(cs) = CString::new(safe) {
                unsafe { *out = cs.into_raw() }
            }
        }
    }
}

// ─── Rust 单元测试 ─────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use std::ffi::CString;

    fn cstr(s: &str) -> CString {
        CString::new(s).unwrap()
    }

    #[test]
    fn test_load_and_evaluate_allow() {
        // 先强制重置为空 PolicySet，避免并行测试的全局状态污染
        let empty_ps = cstr("// empty\n");
        let mut reset_err: *mut c_char = std::ptr::null_mut();
        unsafe { cedar_load_policies(empty_ps.as_ptr(), &mut reset_err) };
        unsafe { cedar_free_string(reset_err) };

        // deny-by-default: 无 permit 策略时全部拒绝
        let p = cstr("Agent::\"agent-1\"");
        let a = cstr("Action::\"infer\"");
        let r = cstr("Resource::\"llm_api\"");
        let ctx = cstr("{\"trust_level\": 3}");
        let mut out: *mut c_char = std::ptr::null_mut();

        let result =
            unsafe { cedar_evaluate(p.as_ptr(), a.as_ptr(), r.as_ptr(), ctx.as_ptr(), &mut out) };
        unsafe { cedar_free_string(out) };
        assert_eq!(result, CEDAR_DENY, "empty policy set should deny");

        // 加载 permit 策略
        let policies = cstr(
            r#"
            permit(
                principal,
                action == Action::"infer",
                resource
            ) when {
                context.trust_level >= 1
            };
        "#,
        );
        let mut err: *mut c_char = std::ptr::null_mut();
        let load_result = unsafe { cedar_load_policies(policies.as_ptr(), &mut err) };
        let err_str = if err.is_null() {
            String::new()
        } else {
            let s = unsafe { CStr::from_ptr(err) }
                .to_str()
                .unwrap_or("")
                .to_string();
            unsafe { cedar_free_string(err) };
            s
        };
        assert_eq!(load_result, 0, "load failed: {err_str}");

        // 再次评估 — 应 ALLOW
        let mut out2: *mut c_char = std::ptr::null_mut();
        let result2 =
            unsafe { cedar_evaluate(p.as_ptr(), a.as_ptr(), r.as_ptr(), ctx.as_ptr(), &mut out2) };
        unsafe { cedar_free_string(out2) };
        assert_eq!(
            result2, CEDAR_ALLOW,
            "should allow after loading permit policy"
        );
    }

    #[test]
    fn test_forbid_overrides_permit() {
        // 同时存在 permit 和 forbid，forbid 应胜出
        let policies = cstr(
            r#"
            permit(principal, action, resource);
            forbid(
                principal,
                action == Action::"delete_data",
                resource
            ) when {
                context.approval_status != "approved"
            };
        "#,
        );
        let mut err: *mut c_char = std::ptr::null_mut();
        let load_result = unsafe { cedar_load_policies(policies.as_ptr(), &mut err) };
        unsafe { cedar_free_string(err) };
        assert_eq!(load_result, 0);

        let p = cstr("Agent::\"agent-1\"");
        let a = cstr("Action::\"delete_data\"");
        let r = cstr("Resource::\"prod_db\"");
        let ctx = cstr("{\"approval_status\": \"pending\"}");
        let mut out: *mut c_char = std::ptr::null_mut();
        let result =
            unsafe { cedar_evaluate(p.as_ptr(), a.as_ptr(), r.as_ptr(), ctx.as_ptr(), &mut out) };
        unsafe { cedar_free_string(out) };
        assert_eq!(result, CEDAR_DENY, "forbid should override permit");
    }

    #[test]
    fn test_policy_count() {
        // 重置为空
        let empty = cstr("");
        let mut err: *mut c_char = std::ptr::null_mut();
        let _ = unsafe { cedar_load_policies(empty.as_ptr(), &mut err) };
        unsafe { cedar_free_string(err) };

        assert_eq!(cedar_policy_count(), 0);
    }

    #[test]
    fn test_free_null_is_safe() {
        // cedar_free_string(null) 不 panic
        unsafe { cedar_free_string(std::ptr::null_mut()) };
    }

    #[test]
    fn test_invalid_utf8_returns_error() {
        let bad: *const c_char = b"\xff\xfe\0".as_ptr() as *const c_char;
        let mut out: *mut c_char = std::ptr::null_mut();
        let a = cstr("Action::\"infer\"");
        let r = cstr("Resource::\"x\"");
        let ctx = cstr("{}");
        let result = unsafe { cedar_evaluate(bad, a.as_ptr(), r.as_ptr(), ctx.as_ptr(), &mut out) };
        unsafe { cedar_free_string(out) };
        assert_eq!(result, CEDAR_ERR_UTF8);
    }
}

// ═══════════════════════════════════════════════════════════════════════════════
// [Storage-SurrealDB-Core] 认知检索轴 FFI
// 架构文档: docs/arch/M02-Storage-Fabric.md §10
//
// 功能: KV + 向量(暴力余弦 MVP) + 图(BFS邻接表) + FTS(TF-IDF倒排)
// 内存: 纯内存实现，进程重启数据丢失；持久化由 Go 侧 SQLite Outbox 投影恢复。
// Tier 1+: 接入真正的 surrealdb crate (kv-rocksdb + 真正 HNSW)。
// ═══════════════════════════════════════════════════════════════════════════════

mod surreal_core {
    use std::cmp::Reverse;
    use std::collections::{BTreeMap, BinaryHeap, HashMap, HashSet, VecDeque};
    use std::sync::{Arc, OnceLock, RwLock};

    // ─── KV Store ─────────────────────────────────────────────────────────────
    pub struct KvStore {
        data: BTreeMap<Vec<u8>, Vec<u8>>,
    }

    impl KvStore {
        pub fn new() -> Self {
            KvStore {
                data: BTreeMap::new(),
            }
        }

        pub fn get(&self, key: &[u8]) -> Option<&Vec<u8>> {
            self.data.get(key)
        }

        pub fn put(&mut self, key: Vec<u8>, val: Vec<u8>) {
            self.data.insert(key, val);
        }

        pub fn delete(&mut self, key: &[u8]) {
            self.data.remove(key);
        }

        pub fn scan_prefix(&self, prefix: &[u8]) -> Vec<(Vec<u8>, Vec<u8>)> {
            let start = prefix.to_vec();
            match prefix_succ(prefix) {
                Some(end) => self
                    .data
                    .range(start..end)
                    .map(|(k, v)| (k.clone(), v.clone()))
                    .collect(),
                None => self
                    .data
                    .range(start..)
                    .map(|(k, v)| (k.clone(), v.clone()))
                    .collect(),
            }
        }
    }

    fn prefix_succ(p: &[u8]) -> Option<Vec<u8>> {
        let mut s = p.to_vec();
        for i in (0..s.len()).rev() {
            s[i] = s[i].wrapping_add(1);
            if s[i] != 0 {
                s.truncate(i + 1);
                return Some(s);
            }
        }
        None
    }

    // ─── Xorshift64 伪随机数（无外部依赖）────────────────────────────────────
    struct Xorshift64 {
        state: u64,
    }
    impl Xorshift64 {
        fn new(seed: u64) -> Self {
            Xorshift64 {
                state: if seed == 0 { 6364136223846793005 } else { seed },
            }
        }
        fn next(&mut self) -> u64 {
            let mut x = self.state;
            x ^= x << 13;
            x ^= x >> 7;
            x ^= x << 17;
            self.state = x;
            x
        }
        fn next_f64(&mut self) -> f64 {
            (self.next() >> 11) as f64 * (1.0 / (1u64 << 53) as f64)
        }
    }

    // f32 bits 比较（正数保序，距离 ∈ [0,2] 满足此条件）
    #[inline]
    fn f32_ord(x: f32) -> u32 {
        x.to_bits()
    }

    // ─── HNSW 索引（Tier 1+；M=16, M0=32, efC=200, ef=50）────────────────────
    const HNSW_M: usize = 16;
    const HNSW_M0: usize = 32;
    const HNSW_EF_C: usize = 200;
    const HNSW_EF: usize = 50;
    // mL = 1/ln(M) — 层高采样参数
    const HNSW_ML: f64 = 0.3606737602222409;

    struct HnswNode {
        id: String,
        embed: Vec<f32>,
        neighbors: Vec<Vec<usize>>, // neighbors[lc] = 第 lc 层邻居 index 列表
    }

    pub struct HnswIndex {
        nodes: Vec<HnswNode>,
        entry_point: Option<usize>,
        max_level: usize,
        id_to_idx: HashMap<String, usize>,
        rng: Xorshift64,
    }

    impl HnswIndex {
        pub fn new() -> Self {
            HnswIndex {
                nodes: Vec::new(),
                entry_point: None,
                max_level: 0,
                id_to_idx: HashMap::new(),
                rng: Xorshift64::new(42),
            }
        }

        // 余弦距离 ∈ [0, 2]（0=完全相同）
        fn cos_dist(a: &[f32], b: &[f32]) -> f32 {
            if a.len() != b.len() {
                return 2.0;
            }
            let dot: f32 = a.iter().zip(b).map(|(x, y)| x * y).sum();
            let na = a.iter().map(|x| x * x).sum::<f32>().sqrt();
            let nb = b.iter().map(|x| x * x).sum::<f32>().sqrt();
            if na < 1e-8 || nb < 1e-8 {
                return 1.0;
            }
            (1.0 - dot / (na * nb)).max(0.0)
        }

        fn random_level(&mut self) -> usize {
            let r = self.rng.next_f64();
            if r <= f64::EPSILON {
                return 0;
            }
            ((-r.ln()) * HNSW_ML).floor() as usize
        }

        // ef-宽 beam search：返回最多 ef 个候选 index（无序）
        fn search_layer(&self, query: &[f32], ep: Vec<usize>, ef: usize, lc: usize) -> Vec<usize> {
            let mut visited: HashSet<usize> = HashSet::with_capacity(ef * 4);
            let mut cands: BinaryHeap<Reverse<(u32, usize)>> = BinaryHeap::new(); // min-heap
            let mut w: BinaryHeap<(u32, usize)> = BinaryHeap::new(); // max-heap，堆顶=最远

            for idx in ep {
                if visited.insert(idx) {
                    let d = f32_ord(Self::cos_dist(query, &self.nodes[idx].embed));
                    cands.push(Reverse((d, idx)));
                    w.push((d, idx));
                }
            }
            while let Some(Reverse((d_c, c_idx))) = cands.pop() {
                let f_d = match w.peek() {
                    Some(&(d, _)) => d,
                    None => break,
                };
                if d_c > f_d {
                    break;
                }
                let nbrs: Vec<usize> = if lc < self.nodes[c_idx].neighbors.len() {
                    self.nodes[c_idx].neighbors[lc].clone()
                } else {
                    Vec::new()
                };
                for e_idx in nbrs {
                    if visited.insert(e_idx) {
                        let e_d = f32_ord(Self::cos_dist(query, &self.nodes[e_idx].embed));
                        let f_d2 = match w.peek() {
                            Some(&(d, _)) => d,
                            None => u32::MAX,
                        };
                        if e_d < f_d2 || w.len() < ef {
                            cands.push(Reverse((e_d, e_idx)));
                            w.push((e_d, e_idx));
                            if w.len() > ef {
                                w.pop();
                            }
                        }
                    }
                }
            }
            w.into_iter().map(|(_, idx)| idx).collect()
        }

        pub fn insert(&mut self, id: String, embed: Vec<f32>) {
            // 更新已有节点
            if let Some(&idx) = self.id_to_idx.get(&id) {
                self.nodes[idx].embed = embed;
                return;
            }
            let l = self.random_level();
            let new_idx = self.nodes.len();
            self.nodes.push(HnswNode {
                id: id.clone(),
                embed,
                neighbors: vec![Vec::new(); l + 1],
            });
            self.id_to_idx.insert(id, new_idx);

            let ep_idx = match self.entry_point {
                None => {
                    self.entry_point = Some(new_idx);
                    self.max_level = l;
                    return;
                }
                Some(ep) => ep,
            };
            let mut ep = vec![ep_idx];

            // Phase 1：从顶层到 l+1，贪婪找单个最近邻
            for lc in (l + 1..=self.max_level).rev() {
                let w = self.search_layer(&self.nodes[new_idx].embed.clone(), ep.clone(), 1, lc);
                let best = w.into_iter().min_by_key(|&i| {
                    f32_ord(Self::cos_dist(
                        &self.nodes[new_idx].embed,
                        &self.nodes[i].embed,
                    ))
                });
                ep = vec![best.unwrap_or(ep_idx)];
            }

            // Phase 2：从 min(l,max_level) 到 0，beam search + 双向连接
            let embed_clone = self.nodes[new_idx].embed.clone();
            for lc in (0..=l.min(self.max_level)).rev() {
                let w = self.search_layer(&embed_clone, ep.clone(), HNSW_EF_C, lc);
                let m_lc = if lc == 0 { HNSW_M0 } else { HNSW_M };

                // 选 m_lc 个最近邻，赋给新节点
                let mut nbrs: Vec<(u32, usize)> = w
                    .iter()
                    .filter(|&&i| i != new_idx)
                    .map(|&i| {
                        (
                            f32_ord(Self::cos_dist(&embed_clone, &self.nodes[i].embed)),
                            i,
                        )
                    })
                    .collect();
                nbrs.sort_unstable_by_key(|&(d, _)| d);
                nbrs.truncate(m_lc);
                self.nodes[new_idx].neighbors[lc] = nbrs.iter().map(|&(_, i)| i).collect();

                // 双向连接：将新节点加入邻居的邻居列表并裁剪
                let nbr_indices: Vec<usize> = nbrs.iter().map(|&(_, i)| i).collect();
                for nb_idx in nbr_indices {
                    let nb_m = if lc == 0 { HNSW_M0 } else { HNSW_M };
                    if self.nodes[nb_idx].neighbors.len() <= lc {
                        self.nodes[nb_idx].neighbors.resize(lc + 1, Vec::new());
                    }
                    self.nodes[nb_idx].neighbors[lc].push(new_idx);
                    if self.nodes[nb_idx].neighbors[lc].len() > nb_m {
                        let nb_embed = self.nodes[nb_idx].embed.clone();
                        let nb_links = self.nodes[nb_idx].neighbors[lc].clone();
                        let mut nb_nbrs: Vec<(u32, usize)> = nb_links
                            .iter()
                            .map(|&i| (f32_ord(Self::cos_dist(&nb_embed, &self.nodes[i].embed)), i))
                            .collect();
                        nb_nbrs.sort_unstable_by_key(|&(d, _)| d);
                        nb_nbrs.truncate(nb_m);
                        self.nodes[nb_idx].neighbors[lc] =
                            nb_nbrs.iter().map(|&(_, i)| i).collect();
                    }
                }
                ep = w;
            }
            if l > self.max_level {
                self.max_level = l;
                self.entry_point = Some(new_idx);
            }
        }

        pub fn knn(&self, query: &[f32], k: usize) -> Vec<(String, f32)> {
            let ep_idx = match self.entry_point {
                Some(e) => e,
                None => return Vec::new(),
            };
            if k == 0 {
                return Vec::new();
            }
            let mut ep = vec![ep_idx];
            // Phase 1：顶层到 layer 1，贪婪
            for lc in (1..=self.max_level).rev() {
                let w = self.search_layer(query, ep.clone(), 1, lc);
                let best = w
                    .into_iter()
                    .min_by_key(|&i| f32_ord(Self::cos_dist(query, &self.nodes[i].embed)));
                ep = vec![best.unwrap_or(ep_idx)];
            }
            // Phase 2：layer 0，beam search
            let ef = k.max(HNSW_EF);
            let w = self.search_layer(query, ep, ef, 0);
            let mut scored: Vec<(u32, usize)> = w
                .iter()
                .map(|&i| (f32_ord(Self::cos_dist(query, &self.nodes[i].embed)), i))
                .collect();
            scored.sort_unstable_by_key(|&(d, _)| d);
            scored.truncate(k);
            scored
                .iter()
                .map(|&(d_bits, i)| {
                    let sim = (1.0_f32 - f32::from_bits(d_bits)).max(-1.0); // cos_sim = 1 - cos_dist
                    (self.nodes[i].id.clone(), sim)
                })
                .collect()
        }
    }

    // ─── Vec Store（双模式：Tier0 暴力扫描 + Tier1+ HNSW）────────────────────
    struct VecRecord {
        id: String,
        embed: Vec<f32>,
    }

    pub struct VecStore {
        records: Vec<VecRecord>, // Tier0 线性扫描（兼作 HNSW 的全量备份）
        hnsw: Option<HnswIndex>, // Tier1+ HNSW 索引（None 时降级暴力扫描）
        pub use_hnsw: bool,
    }

    impl VecStore {
        pub fn new() -> Self {
            VecStore {
                records: Vec::new(),
                hnsw: None,
                use_hnsw: false,
            }
        }

        /// 切换到 HNSW 模式：将现有记录全量导入索引后生效。
        pub fn enable_hnsw(&mut self) {
            if self.hnsw.is_none() {
                let mut idx = HnswIndex::new();
                for r in &self.records {
                    idx.insert(r.id.clone(), r.embed.clone());
                }
                self.hnsw = Some(idx);
            }
            self.use_hnsw = true;
        }

        pub fn upsert(&mut self, id: String, embed: Vec<f32>) {
            if let Some(hnsw) = &mut self.hnsw {
                hnsw.insert(id.clone(), embed.clone());
            }
            match self.records.iter_mut().find(|r| r.id == id) {
                Some(r) => r.embed = embed,
                None => self.records.push(VecRecord { id, embed }),
            }
        }

        pub fn knn(&self, query: &[f32], k: usize) -> Vec<(String, f32)> {
            if self.use_hnsw {
                if let Some(hnsw) = &self.hnsw {
                    return hnsw.knn(query, k);
                }
            }
            // Tier0 fallback：暴力余弦扫描
            if self.records.is_empty() || k == 0 {
                return Vec::new();
            }
            let q_norm = dot_self(query).sqrt();
            let mut scores: Vec<(usize, f32)> = self
                .records
                .iter()
                .enumerate()
                .filter_map(|(i, r)| {
                    if r.embed.len() != query.len() {
                        return None;
                    }
                    let dot: f32 = r.embed.iter().zip(query).map(|(a, b)| a * b).sum();
                    let r_norm = dot_self(&r.embed).sqrt();
                    let sim = if q_norm > 1e-8 && r_norm > 1e-8 {
                        dot / (q_norm * r_norm)
                    } else {
                        0.0
                    };
                    Some((i, sim))
                })
                .collect();
            scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            scores.truncate(k);
            scores
                .into_iter()
                .map(|(i, s)| (self.records[i].id.clone(), s))
                .collect()
        }
    }

    fn dot_self(v: &[f32]) -> f32 {
        v.iter().map(|x| x * x).sum()
    }

    // ─── Graph Store (BFS 邻接表) ──────────────────────────────────────────────
    pub struct GraphStore {
        edges: HashMap<String, Vec<(String, String)>>, // from → [(edge_type, to)]
    }

    impl GraphStore {
        pub fn new() -> Self {
            GraphStore {
                edges: HashMap::new(),
            }
        }

        pub fn relate(&mut self, from: String, edge_type: String, to: String) {
            self.edges.entry(from).or_default().push((edge_type, to));
        }

        // BFS 多跳遍历；edge_type 为空串表示匹配所有类型。
        pub fn traverse(&self, start: &str, edge_type: &str, max_depth: usize) -> Vec<String> {
            let mut visited: HashSet<String> = HashSet::new();
            let mut queue: VecDeque<(String, usize)> = VecDeque::new();
            let mut result: Vec<String> = Vec::new();
            queue.push_back((start.to_string(), 0));
            while let Some((node, depth)) = queue.pop_front() {
                if visited.contains(&node) {
                    continue;
                }
                visited.insert(node.clone());
                if depth > 0 {
                    result.push(node.clone());
                }
                if depth >= max_depth {
                    continue;
                }
                if let Some(nbrs) = self.edges.get(&node) {
                    for (et, to) in nbrs {
                        if (edge_type.is_empty() || et == edge_type) && !visited.contains(to) {
                            queue.push_back((to.clone(), depth + 1));
                        }
                    }
                }
            }
            result
        }
    }

    // ─── FTS Store (TF-IDF 倒排索引) ──────────────────────────────────────────
    pub struct FtsStore {
        index: HashMap<String, HashMap<String, f32>>, // term → {doc_id → tf}
        doc_count: usize,
    }

    impl FtsStore {
        pub fn new() -> Self {
            FtsStore {
                index: HashMap::new(),
                doc_count: 0,
            }
        }

        pub fn index_doc(&mut self, doc_id: &str, text: &str) {
            let terms = tokenize(text);
            if terms.is_empty() {
                return;
            }
            let total = terms.len() as f32;
            let mut tf: HashMap<String, f32> = HashMap::new();
            for t in &terms {
                *tf.entry(t.clone()).or_insert(0.0) += 1.0 / total;
            }
            for (term, score) in tf {
                self.index
                    .entry(term)
                    .or_default()
                    .insert(doc_id.to_string(), score);
            }
            self.doc_count += 1;
        }

        pub fn search(&self, query: &str, k: usize) -> Vec<(String, f32)> {
            let terms = tokenize(query);
            let n = (self.doc_count + 1) as f32;
            let mut scores: HashMap<String, f32> = HashMap::new();
            for term in &terms {
                if let Some(postings) = self.index.get(term) {
                    let df = postings.len() as f32;
                    let idf = (n / (df + 1.0)).ln().max(0.0);
                    for (doc_id, tf) in postings {
                        *scores.entry(doc_id.clone()).or_insert(0.0) += tf * idf;
                    }
                }
            }
            let mut ranked: Vec<(String, f32)> = scores.into_iter().collect();
            ranked.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
            ranked.truncate(k);
            ranked
        }
    }

    fn tokenize(text: &str) -> Vec<String> {
        text.split(|c: char| !c.is_alphanumeric())
            .filter(|s| s.len() >= 2)
            .map(|s| s.to_lowercase())
            .collect()
    }

    // ─── 统一门面 ──────────────────────────────────────────────────────────────
    pub struct SurrealCoreStore {
        pub kv: KvStore,
        pub vec: VecStore,
        pub graph: GraphStore,
        pub fts: FtsStore,
    }

    impl SurrealCoreStore {
        pub fn new() -> Self {
            SurrealCoreStore {
                kv: KvStore::new(),
                vec: VecStore::new(),
                graph: GraphStore::new(),
                fts: FtsStore::new(),
            }
        }
    }

    static STORE: OnceLock<Arc<RwLock<SurrealCoreStore>>> = OnceLock::new();

    pub fn global() -> Arc<RwLock<SurrealCoreStore>> {
        STORE
            .get_or_init(|| Arc::new(RwLock::new(SurrealCoreStore::new())))
            .clone()
    }

    // ─── JSON 序列化（无外部依赖，手动构造）─────────────────────────────────────

    fn bytes_to_hex(b: &[u8]) -> String {
        b.iter().map(|x| format!("{:02x}", x)).collect()
    }

    fn escape_json(s: &str) -> String {
        s.replace('\\', "\\\\").replace('"', "\\\"")
    }

    pub fn encode_kv_pairs(pairs: &[(Vec<u8>, Vec<u8>)]) -> String {
        let mut out = String::from("[");
        for (i, (k, v)) in pairs.iter().enumerate() {
            if i > 0 {
                out.push(',');
            }
            out.push_str(&format!(
                r#"{{"k":"{}","v":"{}"}}"#,
                bytes_to_hex(k),
                bytes_to_hex(v)
            ));
        }
        out.push(']');
        out
    }

    pub fn encode_scored(results: &[(String, f32)]) -> String {
        let mut out = String::from("[");
        for (i, (id, score)) in results.iter().enumerate() {
            if i > 0 {
                out.push(',');
            }
            out.push_str(&format!(
                r#"{{"id":"{}","score":{:.6}}}"#,
                escape_json(id),
                score
            ));
        }
        out.push(']');
        out
    }

    pub fn encode_ids(ids: &[String]) -> String {
        let mut out = String::from("[");
        for (i, id) in ids.iter().enumerate() {
            if i > 0 {
                out.push(',');
            }
            out.push_str(&format!(r#""{}""#, escape_json(id)));
        }
        out.push(']');
        out
    }
}

// ─── Surreal FFI 错误码 ────────────────────────────────────────────────────────
const SURREAL_OK: c_int = 0;
const SURREAL_NOT_FOUND: c_int = 1;
const SURREAL_ERR_UTF8: c_int = -1;
const SURREAL_ERR_LOCK: c_int = -2;
const SURREAL_ERR_PANIC: c_int = -3;

// ─── surreal_open ─────────────────────────────────────────────────────────────

/// 初始化全局 SurrealCoreStore（幂等，多次调用安全）。
#[no_mangle]
pub extern "C" fn surreal_open() -> c_int {
    let _ = surreal_core::global();
    SURREAL_OK
}

// ─── surreal_kv_get ───────────────────────────────────────────────────────────

/// 读取 KV 值。out_val/out_len 指向 Rust 分配的 buffer，须 surreal_free_buf 释放。
/// 返回 0=找到, 1=不存在, 负数=错误
///
/// # Safety
/// key 须为 key_len 字节长的有效内存地址。
#[no_mangle]
pub unsafe extern "C" fn surreal_kv_get(
    key: *const u8,
    key_len: usize,
    out_val: *mut *mut u8,
    out_len: *mut usize,
) -> c_int {
    let key_owned = unsafe { std::slice::from_raw_parts(key, key_len) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let guard = match store.read() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        match guard.kv.get(&key_owned) {
            None => SURREAL_NOT_FOUND,
            Some(val) => {
                let mut boxed = val.clone().into_boxed_slice();
                let ptr = boxed.as_mut_ptr();
                let len = boxed.len();
                std::mem::forget(boxed);
                unsafe {
                    *out_val = ptr;
                    *out_len = len;
                }
                SURREAL_OK
            }
        }
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_kv_put ───────────────────────────────────────────────────────────

/// 写入 KV 键值对。
///
/// # Safety
/// key/val 须为对应 len 字节长的有效内存地址。
#[no_mangle]
pub unsafe extern "C" fn surreal_kv_put(
    key: *const u8,
    key_len: usize,
    val: *const u8,
    val_len: usize,
) -> c_int {
    let k = unsafe { std::slice::from_raw_parts(key, key_len) }.to_vec();
    let v = unsafe { std::slice::from_raw_parts(val, val_len) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        guard.kv.put(k, v);
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_kv_delete ────────────────────────────────────────────────────────

/// 删除 KV 键。
///
/// # Safety
/// key 须为 key_len 字节长的有效内存地址。
#[no_mangle]
pub unsafe extern "C" fn surreal_kv_delete(key: *const u8, key_len: usize) -> c_int {
    let k = unsafe { std::slice::from_raw_parts(key, key_len) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        guard.kv.delete(&k);
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_kv_scan ──────────────────────────────────────────────────────────

/// 前缀扫描，返回 JSON CString，须 surreal_free_string 释放。
/// JSON: [{"k":"<hex>","v":"<hex>"},...]
///
/// # Safety
/// prefix 须为 prefix_len 字节长的有效内存地址。
#[no_mangle]
pub unsafe extern "C" fn surreal_kv_scan(
    prefix: *const u8,
    prefix_len: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    let pfx = unsafe { std::slice::from_raw_parts(prefix, prefix_len) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let guard = match store.read() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        let pairs = guard.kv.scan_prefix(&pfx);
        write_err(out_json, &surreal_core::encode_kv_pairs(&pairs));
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_vec_upsert ───────────────────────────────────────────────────────

/// 写入或更新向量记录。
///
/// # Safety
/// id 须为 NUL-terminated UTF-8；embed 须指向 dim 个 f32。
#[no_mangle]
pub unsafe extern "C" fn surreal_vec_upsert(
    id: *const c_char,
    embed: *const f32,
    dim: usize,
) -> c_int {
    let id_str = match unsafe { CStr::from_ptr(id) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let embed_vec = unsafe { std::slice::from_raw_parts(embed, dim) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        guard.vec.upsert(id_str, embed_vec);
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_vec_knn ──────────────────────────────────────────────────────────

/// K 近邻向量检索，返回 JSON CString，须 surreal_free_string 释放。
/// JSON: [{"id":"<id>","score":<f32>},...]
///
/// # Safety
/// query 须指向 dim 个 f32。
#[no_mangle]
pub unsafe extern "C" fn surreal_vec_knn(
    query: *const f32,
    dim: usize,
    k: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    let q = unsafe { std::slice::from_raw_parts(query, dim) }.to_vec();
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let guard = match store.read() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        let results = guard.vec.knn(&q, k);
        write_err(out_json, &surreal_core::encode_scored(&results));
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_graph_relate ─────────────────────────────────────────────────────

/// 写入一条有向图边 from -[edge_type]-> to。
///
/// # Safety
/// 所有参数须为有效 NUL-terminated UTF-8 C 字符串。
#[no_mangle]
pub unsafe extern "C" fn surreal_graph_relate(
    from_id: *const c_char,
    edge_type: *const c_char,
    to_id: *const c_char,
) -> c_int {
    let from = match unsafe { CStr::from_ptr(from_id) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let et = match unsafe { CStr::from_ptr(edge_type) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let to = match unsafe { CStr::from_ptr(to_id) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        guard.graph.relate(from, et, to);
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_graph_traverse ───────────────────────────────────────────────────

/// BFS 图遍历，返回 JSON CString，须 surreal_free_string 释放。
/// edge_type 为空串表示匹配所有边类型。
/// JSON: ["id1","id2",...]
///
/// # Safety
/// start_id/edge_type 须为有效 NUL-terminated UTF-8 C 字符串。
#[no_mangle]
pub unsafe extern "C" fn surreal_graph_traverse(
    start_id: *const c_char,
    edge_type: *const c_char,
    max_depth: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    let start = match unsafe { CStr::from_ptr(start_id) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let et = match unsafe { CStr::from_ptr(edge_type) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let guard = match store.read() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        let ids = guard.graph.traverse(&start, &et, max_depth);
        write_err(out_json, &surreal_core::encode_ids(&ids));
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_fts_index ────────────────────────────────────────────────────────

/// 将文档写入 FTS 倒排索引。
///
/// # Safety
/// doc_id/text 须为有效 NUL-terminated UTF-8 C 字符串。
#[no_mangle]
pub unsafe extern "C" fn surreal_fts_index(doc_id: *const c_char, text: *const c_char) -> c_int {
    let id = match unsafe { CStr::from_ptr(doc_id) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let txt = match unsafe { CStr::from_ptr(text) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        guard.fts.index_doc(&id, &txt);
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_fts_search ───────────────────────────────────────────────────────

/// BM25-like 全文检索，返回 JSON CString，须 surreal_free_string 释放。
/// JSON: [{"id":"<id>","score":<f32>},...]
///
/// # Safety
/// query 须为有效 NUL-terminated UTF-8 C 字符串。
#[no_mangle]
pub unsafe extern "C" fn surreal_fts_search(
    query: *const c_char,
    k: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    let q = match unsafe { CStr::from_ptr(query) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return SURREAL_ERR_UTF8,
    };
    let result = panic::catch_unwind(move || -> c_int {
        let store = surreal_core::global();
        let guard = match store.read() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        let results = guard.fts.search(&q, k);
        write_err(out_json, &surreal_core::encode_scored(&results));
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── surreal_free_string ──────────────────────────────────────────────────────

/// 释放 surreal_kv_scan / surreal_vec_knn / surreal_graph_traverse / surreal_fts_search
/// 返回的 JSON CString。
///
/// # Safety
/// ptr 须为上述函数分配的指针，或 null。
#[no_mangle]
pub unsafe extern "C" fn surreal_free_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe { drop(CString::from_raw(ptr)) };
    }
}

// ─── surreal_free_buf ─────────────────────────────────────────────────────────

/// 释放 surreal_kv_get 分配的二进制 buffer。
///
/// # Safety
/// ptr 须为 surreal_kv_get 分配的指针，len 须与 out_len 一致，或 ptr 为 null。
#[no_mangle]
pub unsafe extern "C" fn surreal_free_buf(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        unsafe { drop(Box::from_raw(std::ptr::slice_from_raw_parts_mut(ptr, len))) };
    }
}

// ─── surreal_vec_set_mode ─────────────────────────────────────────────────────

/// 设置向量检索模式：0=暴力扫描（Tier0），1=HNSW（Tier1+）。
/// HNSW 首次启用时将现有记录全量导入索引（O(N log N)，启动期一次性开销）。
#[no_mangle]
pub extern "C" fn surreal_vec_set_mode(mode: c_int) -> c_int {
    let result = panic::catch_unwind(|| -> c_int {
        let store = surreal_core::global();
        let mut guard = match store.write() {
            Ok(g) => g,
            Err(_) => return SURREAL_ERR_LOCK,
        };
        if mode == 1 {
            guard.vec.enable_hnsw();
        } else {
            guard.vec.use_hnsw = false;
        }
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

// ─── Surreal 单元测试 ─────────────────────────────────────────────────────────

#[cfg(test)]
mod surreal_tests {
    use super::*;
    use std::ffi::CString;

    fn cs(s: &str) -> CString {
        CString::new(s).unwrap()
    }

    #[test]
    fn test_kv_roundtrip() {
        let k = b"test_key_surreal";
        let v = b"test_value_surreal";
        let ret = unsafe { surreal_kv_put(k.as_ptr(), k.len(), v.as_ptr(), v.len()) };
        assert_eq!(ret, SURREAL_OK);

        let mut out_val: *mut u8 = std::ptr::null_mut();
        let mut out_len: usize = 0;
        let ret2 = unsafe { surreal_kv_get(k.as_ptr(), k.len(), &mut out_val, &mut out_len) };
        assert_eq!(ret2, SURREAL_OK);
        let got = unsafe { std::slice::from_raw_parts(out_val, out_len) }.to_vec();
        unsafe { surreal_free_buf(out_val, out_len) };
        assert_eq!(got, v);
    }

    #[test]
    fn test_kv_not_found() {
        let k = b"__nonexistent_key__";
        let mut p: *mut u8 = std::ptr::null_mut();
        let mut l: usize = 0;
        let ret = unsafe { surreal_kv_get(k.as_ptr(), k.len(), &mut p, &mut l) };
        assert_eq!(ret, SURREAL_NOT_FOUND);
    }

    #[test]
    fn test_kv_scan() {
        let pfx = b"scan_prefix_";
        unsafe {
            surreal_kv_put(pfx.as_ptr(), pfx.len(), b"v1".as_ptr(), 2);
            let k2 = b"scan_prefix_b";
            surreal_kv_put(k2.as_ptr(), k2.len(), b"v2".as_ptr(), 2);
        }
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_kv_scan(pfx.as_ptr(), pfx.len(), &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        assert!(json.starts_with('['), "json should be array: {json}");
    }

    #[test]
    fn test_vec_knn() {
        let id1 = cs("doc-vec-1");
        let id2 = cs("doc-vec-2");
        let e1: Vec<f32> = vec![1.0, 0.0, 0.0];
        let e2: Vec<f32> = vec![0.0, 1.0, 0.0];
        unsafe {
            surreal_vec_upsert(id1.as_ptr(), e1.as_ptr(), e1.len());
            surreal_vec_upsert(id2.as_ptr(), e2.as_ptr(), e2.len());
        }
        let query: Vec<f32> = vec![1.0, 0.0, 0.0];
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_vec_knn(query.as_ptr(), query.len(), 2, &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        // doc-vec-1 应排在最前（余弦相似度 = 1.0）
        assert!(
            json.contains("doc-vec-1"),
            "knn should return doc-vec-1: {json}"
        );
    }

    #[test]
    fn test_graph_traverse() {
        let a = cs("node-a");
        let b = cs("node-b");
        let c_node = cs("node-c");
        let et = cs("link");
        unsafe {
            surreal_graph_relate(a.as_ptr(), et.as_ptr(), b.as_ptr());
            surreal_graph_relate(b.as_ptr(), et.as_ptr(), c_node.as_ptr());
        }
        let empty_et = cs("");
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_graph_traverse(a.as_ptr(), empty_et.as_ptr(), 3, &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        assert!(
            json.contains("node-b"),
            "traverse should reach node-b: {json}"
        );
        assert!(
            json.contains("node-c"),
            "traverse should reach node-c: {json}"
        );
    }

    #[test]
    fn test_fts_search() {
        let doc1 = cs("fts-doc-1");
        let doc2 = cs("fts-doc-2");
        let text1 = cs("the quick brown fox jumps over the lazy dog");
        let text2 = cs("machine learning deep neural network");
        let q = cs("quick fox");
        unsafe {
            surreal_fts_index(doc1.as_ptr(), text1.as_ptr());
            surreal_fts_index(doc2.as_ptr(), text2.as_ptr());
        }
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_fts_search(q.as_ptr(), 5, &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        assert!(
            json.contains("fts-doc-1"),
            "fts should return fts-doc-1: {json}"
        );
    }

    #[test]
    fn test_free_null_safe() {
        unsafe {
            surreal_free_string(std::ptr::null_mut());
            surreal_free_buf(std::ptr::null_mut(), 0);
        }
    }

    // ─── HNSW 专项测试（Tier 1+）─────────────────────────────────────────────

    #[test]
    fn test_hnsw_knn_accuracy() {
        // 切换到 HNSW 模式
        let rc = surreal_vec_set_mode(1);
        assert_eq!(rc, SURREAL_OK, "enable HNSW failed");

        // 写入 10 个正交方向的向量
        let dim = 4usize;
        let ids: Vec<_> = (0..8).map(|i| cs(&format!("hnsw-v{i}"))).collect();
        let embeds: Vec<Vec<f32>> = vec![
            vec![1.0, 0.0, 0.0, 0.0],
            vec![0.0, 1.0, 0.0, 0.0],
            vec![0.0, 0.0, 1.0, 0.0],
            vec![0.0, 0.0, 0.0, 1.0],
            vec![0.7, 0.7, 0.0, 0.0],
            vec![0.0, 0.7, 0.7, 0.0],
            vec![0.0, 0.0, 0.7, 0.7],
            vec![0.7, 0.0, 0.0, 0.7],
        ];
        for (id, embed) in ids.iter().zip(embeds.iter()) {
            let ret = unsafe { surreal_vec_upsert(id.as_ptr(), embed.as_ptr(), dim) };
            assert_eq!(ret, SURREAL_OK);
        }

        // 查询最接近 [1,0,0,0] 的向量，hnsw-v0 和 hnsw-v4 应排前两名
        let query: Vec<f32> = vec![1.0, 0.0, 0.0, 0.0];
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_vec_knn(query.as_ptr(), dim, 2, &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        assert!(
            json.contains("hnsw-v0"),
            "HNSW KNN: hnsw-v0 not in top2: {json}"
        );

        // 恢复暴力扫描模式
        let rc = surreal_vec_set_mode(0);
        assert_eq!(rc, SURREAL_OK, "disable HNSW failed");
    }

    #[test]
    fn test_hnsw_upsert_update() {
        let rc = surreal_vec_set_mode(1);
        assert_eq!(rc, SURREAL_OK);
        let id = cs("hnsw-update-target");
        let e1: Vec<f32> = vec![1.0, 0.0, 0.0];
        let e2: Vec<f32> = vec![0.0, 0.0, 1.0];
        unsafe { surreal_vec_upsert(id.as_ptr(), e1.as_ptr(), 3) };
        // 更新 embedding
        unsafe { surreal_vec_upsert(id.as_ptr(), e2.as_ptr(), 3) };

        let query: Vec<f32> = vec![0.0, 0.0, 1.0];
        let mut out: *mut c_char = std::ptr::null_mut();
        let ret = unsafe { surreal_vec_knn(query.as_ptr(), 3, 1, &mut out) };
        assert_eq!(ret, SURREAL_OK);
        let json = unsafe { std::ffi::CStr::from_ptr(out) }
            .to_str()
            .unwrap()
            .to_string();
        unsafe { surreal_free_string(out) };
        assert!(json.contains("hnsw-update-target"), "upsert update: {json}");
        surreal_vec_set_mode(0);
    }
}
