use std::ffi::{CStr, CString};
use std::os::raw::{c_char, c_int};
use std::panic;
use std::sync::{Arc, OnceLock, RwLock};

use surrealdb::engine::local::{Db, Mem, RocksDb};
use surrealdb::Surreal;
use tokio::runtime::Runtime;

// ─── Surreal FFI 错误码 ────────────────────────────────────────────────────────
const SURREAL_OK: c_int = 0;
const SURREAL_NOT_FOUND: c_int = 1;

const SURREAL_ERR_PANIC: c_int = -3;

pub struct SurrealTier1Store {
    pub db: Surreal<Db>,
    pub rt: Runtime,
    pub use_hnsw: bool,
}

impl SurrealTier1Store {
    pub fn new(tier: i32, db_path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let rt = tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()?;
        let db = rt.block_on(async {
            if tier >= 1 && !db_path.is_empty() {
                Surreal::new::<RocksDb>(db_path).await
            } else {
                Surreal::new::<Mem>(()).await
            }
        })?;
        rt.block_on(async { db.use_ns("polaris").use_db("cognition").await })?;

        // Define vector table and index if using HNSW
        rt.block_on(async {
            let _ = db.query("DEFINE TABLE vectors SCHEMAFULL; DEFINE FIELD embed ON vectors TYPE array<float>; DEFINE INDEX hnsw_idx ON vectors FIELDS embed MTREE DIMENSION 4 DISTANCE COSINE;").await;
        });

        Ok(SurrealTier1Store {
            db,
            rt,
            use_hnsw: false,
        })
    }
}

static STORE_TIER1: OnceLock<Arc<RwLock<SurrealTier1Store>>> = OnceLock::new();

fn write_err(out_json: *mut *mut c_char, s: &str) {
    if !out_json.is_null() {
        unsafe { *out_json = CString::new(s).unwrap().into_raw() };
    }
}

fn bytes_to_hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{:02x}", x)).collect()
}

fn hex_to_bytes(s: &str) -> Result<Vec<u8>, std::num::ParseIntError> {
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16))
        .collect()
}

#[no_mangle]
pub unsafe extern "C" fn surreal_open(tier: c_int, db_path: *const c_char) -> c_int {
    let path = if db_path.is_null() {
        "".to_string()
    } else {
        unsafe { CStr::from_ptr(db_path) }
            .to_str()
            .unwrap_or("")
            .to_string()
    };

    let result = panic::catch_unwind(|| {
        STORE_TIER1.get_or_init(|| {
            Arc::new(RwLock::new(
                SurrealTier1Store::new(tier, &path).expect("failed to init tier1 db"),
            ))
        });
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

#[no_mangle]
pub unsafe extern "C" fn surreal_kv_get(
    key: *const u8,
    key_len: usize,
    out_val: *mut *mut u8,
    out_len: *mut usize,
) -> c_int {
    let key_owned = unsafe { std::slice::from_raw_parts(key, key_len) }.to_vec();
    let key_hex = bytes_to_hex(&key_owned);
    let result = panic::catch_unwind(|| {
        let store_arc = STORE_TIER1.get().unwrap();
        let guard = store_arc.read().unwrap();

        // Query SurrealDB
        let res: Option<String> = guard.rt.block_on(async {
            let mut response = guard
                .db
                .query("SELECT v FROM kv WHERE k = $k")
                .bind(("k", key_hex))
                .await
                .ok()?;
            let vals: Vec<surrealdb::sql::Value> = response.take(0).ok()?;
            if vals.is_empty() {
                return None;
            }
            if let surrealdb::sql::Value::Object(obj) = &vals[0] {
                if let Some(surrealdb::sql::Value::Strand(s)) = obj.get("v") {
                    return Some(s.clone().as_string());
                }
            }
            None
        });

        match res {
            None => SURREAL_NOT_FOUND,
            Some(hex_str) => {
                let val_bytes = hex_to_bytes(&hex_str).unwrap_or_default();
                let mut boxed = val_bytes.into_boxed_slice();
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

#[no_mangle]
pub unsafe extern "C" fn surreal_kv_put(
    key: *const u8,
    key_len: usize,
    val: *const u8,
    val_len: usize,
) -> c_int {
    let k = bytes_to_hex(unsafe { std::slice::from_raw_parts(key, key_len) });
    let v = bytes_to_hex(unsafe { std::slice::from_raw_parts(val, val_len) });
    let result = panic::catch_unwind(|| {
        let store_arc = STORE_TIER1.get().unwrap();
        let guard = store_arc.read().unwrap();
        guard.rt.block_on(async {
            let _ = guard
                .db
                .query("UPSERT kv SET k = $k, v = $v")
                .bind(("k", k))
                .bind(("v", v))
                .await;
        });
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

#[no_mangle]
pub unsafe extern "C" fn surreal_kv_delete(key: *const u8, key_len: usize) -> c_int {
    let k = bytes_to_hex(unsafe { std::slice::from_raw_parts(key, key_len) });
    let result = panic::catch_unwind(|| {
        let store_arc = STORE_TIER1.get().unwrap();
        let guard = store_arc.read().unwrap();
        guard.rt.block_on(async {
            let _ = guard
                .db
                .query("DELETE kv WHERE k = $k")
                .bind(("k", k))
                .await;
        });
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

#[no_mangle]
pub unsafe extern "C" fn surreal_kv_scan(
    _prefix: *const u8,
    _prefix_len: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    // For simplicity, fallback empty array for prefix scan currently
    write_err(out_json, "[]");
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_vec_upsert(
    id: *const c_char,
    embed: *const f32,
    dim: usize,
) -> c_int {
    let id_str = unsafe { CStr::from_ptr(id) }.to_str().unwrap().to_string();
    let embed_vec = unsafe { std::slice::from_raw_parts(embed, dim) }.to_vec();
    let result = panic::catch_unwind(|| {
        let store_arc = STORE_TIER1.get().unwrap();
        let guard = store_arc.read().unwrap();
        guard.rt.block_on(async {
            let _ = guard
                .db
                .query("UPSERT vectors SET id = $id, embed = $embed")
                .bind(("id", id_str))
                .bind(("embed", embed_vec))
                .await;
        });
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}

#[no_mangle]
pub unsafe extern "C" fn surreal_vec_knn(
    _query: *const f32,
    _dim: usize,
    _k: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    // Basic stub return
    write_err(out_json, "[]");
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_graph_relate(
    _from_id: *const c_char,
    _edge_type: *const c_char,
    _to_id: *const c_char,
) -> c_int {
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_graph_traverse(
    _start_id: *const c_char,
    _edge_type: *const c_char,
    _max_depth: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    write_err(out_json, "[]");
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_fts_index(_doc_id: *const c_char, _text: *const c_char) -> c_int {
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_fts_search(
    _query: *const c_char,
    _k: usize,
    out_json: *mut *mut c_char,
) -> c_int {
    write_err(out_json, "[]");
    SURREAL_OK
}

#[no_mangle]
pub unsafe extern "C" fn surreal_free_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe { drop(CString::from_raw(ptr)) };
    }
}

#[no_mangle]
pub unsafe extern "C" fn surreal_free_buf(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        unsafe { drop(Box::from_raw(std::ptr::slice_from_raw_parts_mut(ptr, len))) };
    }
}

#[no_mangle]
pub extern "C" fn surreal_vec_set_mode(mode: c_int) -> c_int {
    let result = panic::catch_unwind(|| {
        let store_arc = STORE_TIER1.get().unwrap();
        let mut guard = store_arc.write().unwrap();
        guard.use_hnsw = mode == 1;
        SURREAL_OK
    });
    result.unwrap_or(SURREAL_ERR_PANIC)
}
