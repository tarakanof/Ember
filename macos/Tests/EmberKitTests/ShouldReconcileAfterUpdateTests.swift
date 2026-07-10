import Testing
@testable import EmberKit

@Test func shouldReconcileWhenNoPriorVersionRecorded() {
    #expect(shouldReconcileAfterUpdate(currentVersion: "1.0", lastReconciledVersion: nil) == true)
}

@Test func shouldNotReconcileWhenVersionUnchanged() {
    #expect(shouldReconcileAfterUpdate(currentVersion: "1.0", lastReconciledVersion: "1.0") == false)
}

@Test func shouldReconcileWhenVersionChanged() {
    #expect(shouldReconcileAfterUpdate(currentVersion: "2.0", lastReconciledVersion: "1.0") == true)
}
