import 'package:flutter/material.dart';

class CodeEditorScreen extends StatelessWidget {
  const CodeEditorScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      color: const Color(0xFF1E1E1E),
      padding: const EdgeInsets.all(16.0),
      child: const SingleChildScrollView(
        child: SelectableText.rich(
          TextSpan(
            style: TextStyle(fontFamily: 'monospace', fontSize: 14.0),
            children: [
              TextSpan(
                text: '// DevOrbit Remote Code Editor\n',
                style: TextStyle(color: Colors.grey),
              ),
              TextSpan(
                text: 'package ',
                style: TextStyle(color: Colors.purpleAccent),
              ),
              TextSpan(
                text: 'com.devorbit.mobile\n\n',
                style: TextStyle(color: Colors.white),
              ),
              TextSpan(
                text: 'fun ',
                style: TextStyle(color: Colors.blueAccent),
              ),
              TextSpan(
                text: 'main',
                style: TextStyle(color: Colors.yellowAccent),
              ),
              TextSpan(
                text: '() {\n    println(',
                style: TextStyle(color: Colors.white),
              ),
              TextSpan(
                text: '"DevOrbit Connected to Remote Host!"',
                style: TextStyle(color: Colors.greenAccent),
              ),
              TextSpan(
                text: ')\n}',
                style: TextStyle(color: Colors.white),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
