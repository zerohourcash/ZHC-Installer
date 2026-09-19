import Foundation

@main struct ModelChecks {
    @MainActor static func main() {
        let model = InstallerModel()
        precondition(!model.running && !model.complete)
        model.start()
        precondition(!model.running && model.error == nil, "must require the replacement checkbox")
        func event(_ json: String) { model.receive(Data(json.utf8), isEvents:true) }
        event(#"{"type":"progress","phase":"snapshot_download","done":25,"total":100,"speed":5}"#)
        precondition(model.stageIndex == 1 && model.fraction == 0.25 && model.speed == 5)
        event(#"{"type":"phase","phase":"snapshot_verify"}"#)
        precondition(model.stageIndex == 2 && model.fraction == nil && model.speed == 0)
        event(#"{"type":"progress","phase":"snapshot_extract","done":60,"total":100}"#)
        precondition(model.stageIndex == 3 && model.fraction == 0.6)
        event(#"{"type":"phase","phase":"node_download"}"#)
        event(#"{"type":"phase","phase":"package_verify"}"#)
        precondition(model.stageIndex == 4)
        event(#"{"type":"phase","phase":"node_readiness"}"#)
        precondition(model.stageIndex == 5 && model.fraction == nil)
        event(#"{"type":"complete"}"#)
        precondition(!model.complete, "completion requires process exit success as well")
        event(#"{"type":"error","message":"Fixture checksum mismatch"}"#)
        precondition(model.error == "Fixture checksum mismatch")
        model.confirmedReplacement = true
        model.start()
        precondition(!model.running && model.error != nil, "test bundle has no helper/payload: fail closed")
        print("PASS: real progress, phase reset, completion gate, errors and missing-payload preflight")
    }
}
