use std::{
    env, fs,
    path::{Path, PathBuf},
};
fn collect(dir: &Path, root: &Path, out: &mut Vec<(String, PathBuf)>) {
    let mut paths: Vec<_> = fs::read_dir(dir)
        .expect("read built frontend")
        .map(|p| p.expect("read frontend entry").path())
        .collect();
    paths.sort();
    for p in paths {
        if p.is_dir() {
            collect(&p, root, out)
        } else if p.is_file() {
            out.push((
                p.strip_prefix(root)
                    .unwrap()
                    .to_string_lossy()
                    .replace('\\', "/"),
                p.canonicalize().unwrap(),
            ))
        }
    }
}
fn main() {
    let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap())
        .join("../../..")
        .join("cmd/tam-client/dist");
    println!("cargo:rerun-if-changed={}", root.display());
    assert!(
        root.join("index.html").is_file(),
        "Built Svelte assets are missing. Run pnpm install --frozen-lockfile and pnpm build in frontend/ before building R-TAM."
    );
    let mut files = vec![];
    collect(&root, &root, &mut files);
    let mut source = String::from("pub static FILES: &[(&str, &[u8])] = &[\n");
    for (name, path) in files {
        source.push_str(&format!(
            "({name:?}, include_bytes!({:?})),\n",
            path.to_string_lossy()
        ))
    }
    source.push_str("];\n");
    fs::write(
        PathBuf::from(env::var_os("OUT_DIR").unwrap()).join("assets.rs"),
        source,
    )
    .unwrap();
}
