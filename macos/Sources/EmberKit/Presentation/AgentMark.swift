import CoreGraphics

public enum AgentMark {
    public static func image(_ tool: String) -> CGImage? { KnobFaceRender.toolMarks[tool] }
}
