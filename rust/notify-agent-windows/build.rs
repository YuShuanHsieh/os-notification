fn main() {
    // embed_resource doesn't emit its own rerun-if-changed (see its docs), and once ANY
    // rerun-if-changed is emitted, Cargo stops watching the whole package directory by
    // default — so both inputs need to be declared explicitly here.
    println!("cargo:rerun-if-changed=icon.rc");
    println!("cargo:rerun-if-changed=assets/app.ico");

    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        embed_resource::compile("icon.rc", embed_resource::NONE);
    }
}
