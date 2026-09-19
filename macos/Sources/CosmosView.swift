import SwiftUI

struct CosmosView: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var active: Bool
    var body: some View {
        TimelineView(.animation(minimumInterval: active ? 1.0/30 : 1.0/20, paused: reduceMotion)) { timeline in
            let t = reduceMotion ? 0 : timeline.date.timeIntervalSinceReferenceDate
            Canvas { context, size in
                let center = CGPoint(x: size.width * 0.66, y: size.height * 0.44)
                let radius = min(size.width, size.height) * 0.245
                context.fill(Path(CGRect(origin: .zero, size: size)), with: .linearGradient(
                    Gradient(colors: [Color(red:0.025,green:0.035,blue:0.10), Color(red:0.045,green:0.055,blue:0.16), .black]),
                    startPoint: .zero, endPoint: CGPoint(x:size.width,y:size.height)))
                let halo = CGRect(x:center.x-radius*2.1,y:center.y-radius*2.1,width:radius*4.2,height:radius*4.2)
                context.fill(Path(ellipseIn:halo), with:.radialGradient(Gradient(colors:[.cyan.opacity(0.12),.blue.opacity(0.07),.clear]),center:center,startRadius:radius*0.2,endRadius:radius*2.1))
                for i in 0..<190 {
                    let seed = Double(i)
                    let x = (seed * 0.61803398875).truncatingRemainder(dividingBy:1) * size.width
                    let y = ((seed * 0.381966 + sin(seed*9.2)*0.2 + 1).truncatingRemainder(dividingBy:1)) * size.height
                    let twinkle = 0.25 + 0.5 * (sin(t*0.65 + seed*3.7)+1)/2
                    let r = i % 11 == 0 ? 1.5 : 0.65
                    context.fill(Path(ellipseIn:CGRect(x:x,y:y,width:r*2,height:r*2)),with:.color(.white.opacity(twinkle)))
                }
                var orbital = context
                orbital.translateBy(x:center.x,y:center.y)
                orbital.rotate(by:.degrees(-25))
                for ring in 0..<3 {
                    let rx = radius*(1.42 + Double(ring)*0.26)
                    let ry = rx*0.38
                    orbital.stroke(Path(ellipseIn:CGRect(x:-rx,y:-ry,width:rx*2,height:ry*2)),with:.color(.cyan.opacity(0.13-Double(ring)*0.025)),lineWidth:0.8)
                    let angle = t*(active ? 0.26 : 0.10)*(ring%2 == 0 ? 1 : -1)+Double(ring)*2
                    let point = CGPoint(x:cos(angle)*rx,y:sin(angle)*ry)
                    orbital.fill(Path(ellipseIn:CGRect(x:point.x-3,y:point.y-3,width:6,height:6)),with:.color(.cyan.opacity(0.85)))
                }
                let planet = CGRect(x:center.x-radius,y:center.y-radius,width:radius*2,height:radius*2)
                context.fill(Path(ellipseIn:planet),with:.radialGradient(Gradient(colors:[Color(red:0.22,green:0.50,blue:0.61),Color(red:0.055,green:0.17,blue:0.27),Color(red:0.008,green:0.023,blue:0.06)]),center:CGPoint(x:center.x-radius*0.55,y:center.y-radius*0.55),startRadius:0,endRadius:radius*1.8))
                var surface = context
                surface.clip(to:Path(ellipseIn:planet))
                for row in 0..<24 {
                    let y = center.y-radius + Double(row)*radius/12
                    var line = Path()
                    for column in 0...70 {
                        let x = center.x-radius+Double(column)*radius/35
                        let wave = sin(Double(column)*0.25+Double(row)*0.6+t*0.07)*4 + cos(Double(column)*0.13+Double(row))*7
                        if column == 0 { line.move(to:CGPoint(x:x,y:y+wave)) } else { line.addLine(to:CGPoint(x:x,y:y+wave)) }
                    }
                    surface.stroke(line,with:.color(.cyan.opacity(row%3 == 0 ? 0.13 : 0.055)),lineWidth:row%3 == 0 ? 2 : 1)
                }
                context.stroke(Path(ellipseIn:planet),with:.linearGradient(Gradient(colors:[.cyan.opacity(0.7),.blue.opacity(0.05),.clear]),startPoint:CGPoint(x:center.x-radius,y:center.y-radius),endPoint:CGPoint(x:center.x+radius,y:center.y+radius)),lineWidth:1.4)
            }
        }
        .accessibilityHidden(true)
    }
}
